# Phase A flow-lane collector image (doc 15 §4). The binary is fully static
# (CGO-free), so a scratch base needs nothing external — no registry pull, smallest
# possible privileged surface. /proc is a kernel mount at runtime, not in the image.
#
# Build (host = linux/arm64 kind nodes):
#   GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o deploy/flow/conntrack-agent ./obsd/cmd/conntrack-agent
#   docker build -t vigil-flow-agent:v0 deploy/flow/
#   kind load docker-image vigil-flow-agent:v0 --name vigil
FROM scratch
COPY conntrack-agent /conntrack-agent
ENTRYPOINT ["/conntrack-agent"]
