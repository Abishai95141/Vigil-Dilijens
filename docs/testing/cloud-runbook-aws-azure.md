# Cloud runbook — single-node k3s on AWS / Azure

> Same theme-aligned test bed as [cloud-runbook-gcp.md](cloud-runbook-gcp.md) — one
> generously-sized VM packed with pods, closer to the ABB Theme-2 "single-node edge /
> industrial cluster" than a managed multi-node cluster — but on **AWS** or **Azure**.
>
> Only the VM lifecycle (create / SSH / firewall / teardown) is provider-specific.
> Everything else — k3s with a raised pod cap, the toolchain, the node-exporter + KSM
> + ABB-digital-twin deploy, and the live tiers — is one provider-agnostic command:
> **`deploy/cloud/bootstrap-vigil-edge.sh`**. Pick a provider below, then jump to §3.
>
> **Security (both):** obsd's `:9095` is unauthenticated by default — the security
> group / NSG opens **SSH only**. Reach the API by SSH tunnel (§4), never publicly.
> **Cost:** ~$0.38/hr (AWS m6i.2xlarge) / ~$0.46/hr (Azure D8s v5) — **tear down** (§5).

---

## A. AWS — `m6i.2xlarge` (8 vCPU / 32 GB)

### 1. Launch the instance (SSH-only security group)

```bash
REGION=ap-south-1
# newest Canonical Ubuntu 24.04 LTS AMI for the region
AMI=$(aws ec2 describe-images --region $REGION --owners 099720109477 \
  --filters "Name=name,Values=ubuntu/images/hvm-ssd*/ubuntu-noble-24.04-amd64-server-*" \
            "Name=state,Values=available" \
  --query 'reverse(sort_by(Images,&CreationDate))[0].ImageId' --output text)

aws ec2 create-key-pair --region $REGION --key-name vigil-edge \
  --query KeyMaterial --output text > vigil-edge.pem && chmod 600 vigil-edge.pem

SG=$(aws ec2 create-security-group --region $REGION --group-name vigil-edge-sg \
  --description "Vigil edge: SSH only" --query GroupId --output text)
MYIP=$(curl -s https://checkip.amazonaws.com)
aws ec2 authorize-security-group-ingress --region $REGION --group-id $SG \
  --protocol tcp --port 22 --cidr ${MYIP}/32      # SSH from your IP only; :9095 stays private

aws ec2 run-instances --region $REGION --image-id $AMI --instance-type m6i.2xlarge \
  --key-name vigil-edge --security-group-ids $SG --count 1 \
  --block-device-mappings '[{"DeviceName":"/dev/sda1","Ebs":{"VolumeSize":80,"VolumeType":"gp3"}}]' \
  --tag-specifications 'ResourceType=instance,Tags=[{Key=Name,Value=vigil-edge}]'
```

### 2. SSH in

```bash
IP=$(aws ec2 describe-instances --region $REGION \
  --filters "Name=tag:Name,Values=vigil-edge" "Name=instance-state-name,Values=running" \
  --query 'Reservations[0].Instances[0].PublicIpAddress' --output text)
ssh -i vigil-edge.pem ubuntu@$IP
```

→ jump to **§3 (common)**. Tunnel (§4) uses: `ssh -i vigil-edge.pem -L 9095:localhost:9095 ubuntu@$IP`.

### 5A. Tear down (AWS)

```bash
IID=$(aws ec2 describe-instances --region $REGION --filters "Name=tag:Name,Values=vigil-edge" \
  --query 'Reservations[0].Instances[0].InstanceId' --output text)
aws ec2 terminate-instances --region $REGION --instance-ids $IID
aws ec2 wait instance-terminated --region $REGION --instance-ids $IID
aws ec2 delete-security-group --region $REGION --group-id $SG
aws ec2 delete-key-pair --region $REGION --key-name vigil-edge && rm -f vigil-edge.pem
```

---

## B. Azure — `Standard_D8s_v5` (8 vCPU / 32 GB)

### 1. Launch the VM (SSH-only by default)

```bash
az group create -n vigil-edge-rg -l centralindia
az vm create -g vigil-edge-rg -n vigil-edge \
  --image Ubuntu2404 --size Standard_D8s_v5 \
  --admin-username azureuser --generate-ssh-keys \
  --os-disk-size-gb 80 --storage-sku Premium_LRS --public-ip-sku Standard
# `az vm create` opens ONLY port 22. Do NOT `az vm open-port` for :9095 — keep it private.
```

### 2. SSH in

```bash
IP=$(az vm show -g vigil-edge-rg -n vigil-edge -d --query publicIps -o tsv)
ssh azureuser@$IP
```

→ jump to **§3 (common)**. Tunnel (§4) uses: `ssh -L 9095:localhost:9095 azureuser@$IP`.

### 5B. Tear down (Azure)

```bash
az group delete -n vigil-edge-rg --yes --no-wait    # removes the VM, disk, NIC, NSG, public IP
```

---

## 3. Common — bootstrap + run the tiers (both providers)

On the VM:

```bash
sudo apt-get update && sudo apt-get install -y git
git clone https://github.com/Abishai95141/Vigil-Dilijens && cd Vigil-Dilijens

# one command: k3s (--max-pods=300) + toolchain + obsd build + node-exporter/KSM/ABB twin,
# then e2e + scale(N=300) + bench. Soak is separate (run it overnight, below).
MAX_PODS=300 RUN_TIERS=1 ./deploy/cloud/bootstrap-vigil-edge.sh

export VIGIL_TEST_KUBECONFIG=$HOME/k3s.yaml
VIGIL_SOAK_DURATION=2h just soak     # leak/stability over time — run overnight
```

Brutal ABB chaos against the live twin (node-exporter makes PSI observable, so
`STORAGE_SATURATION` fires for real — the signal a bare laptop k3s can't surface):

```bash
kubectl apply -f corpus/chaos/industrial/abb-mqtt-leak-oom-reconnect-cpu.yaml
kubectl apply -f corpus/chaos/industrial/abb-historian-io-scada-restart.yaml
bin/obsd --kubeconfig $HOME/k3s.yaml --ksm-enabled --app-metrics-enabled --health-addr :9095 &
```

## 4. Access the API safely (never expose :9095)

From your laptop, SSH-tunnel (see each provider's §2 for the exact command), then:

```bash
curl localhost:9095/api/coverage          # over the tunnel
```

If it must be reachable on a private network instead, use bearer auth:
`bin/obsd ... --api-token "$(openssl rand -hex 24)"` (deny-by-default on `/api` + `/mcp`).

## 6. The determinism / portability dividend (the strong demo)

Identical to the GCP runbook §7 — capture a window on the cloud node, pull the bundle
home, `bin/replay -bundle ./cap` reproduces the **same digests byte-for-byte**. The
provider-specific copy command:

```bash
# AWS:   scp -i vigil-edge.pem -r ubuntu@$IP:/tmp/cap ./cloud-cap
# Azure: scp -r azureuser@$IP:/tmp/cap ./cloud-cap
bin/replay -bundle ./cloud-cap
```
