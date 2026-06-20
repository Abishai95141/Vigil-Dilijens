# vigil-abb-sim — one image, five roles (ROLE env: sensors|gateway|stream|analyzer|assetapi).
# Built locally and loaded into kind via `kind load docker-image vigil-abb-sim:0.1`
# (no registry). This is a simulation sidecar image — NOT part of obsd; the Go
# runtime stays CGO-free and pure-Go regardless.
#
# Real client libs are baked in so the data path is genuine with zero per-pod pip:
#   paho-mqtt        -> Edgenius MQTT broker (Mosquitto)
#   influxdb-client  -> Genix historian (InfluxDB 2.x)
#   psycopg2-binary  -> asset registry (Postgres)
#   requests         -> HTTP polling between sims
FROM python:3.12-slim

# psycopg2-binary ships its own libpq; no build toolchain needed.
# paho-mqtt pinned to the 1.6.x API (the callback signatures in sim.py are VERSION1;
# paho 2.0 made CallbackAPIVersion a required ctor arg and changed them).
RUN pip install --no-cache-dir \
        paho-mqtt==1.6.1 \
        influxdb-client==1.43.0 \
        psycopg2-binary==2.9.9 \
        minio==7.2.8 \
        requests==2.32.3

WORKDIR /app
COPY sim.py /app/sim.py

# ROLE is set per-Deployment in the manifests; default keeps `docker run` sane.
ENV ROLE=sensors PORT=8080 PYTHONUNBUFFERED=1
EXPOSE 8080
ENTRYPOINT ["python3", "/app/sim.py"]
