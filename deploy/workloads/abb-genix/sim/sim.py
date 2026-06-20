#!/usr/bin/env python3
# ABB Ability Genix predictive-maintenance simulation — single role-parameterized service.
#
# One image, five roles (ROLE env): sensors | gateway | stream | analyzer | assetapi.
# Together they form the documented ABB Ability data path:
#   smart-sensors -> opcua-gateway -> edgenius-broker(MQTT) -> stream-processor
#     -> genix-historian(InfluxDB) [+ genix-datalake(MinIO)] -> pdm-analyzer
#     -> asset-registry(Postgres) -> asset-api -> operations-dashboard(Grafana)
#
# Vigil-facing contract (mirrors corpus/chaos/app-signal-capA.yaml):
#   * GET /metrics  -> LABEL-FREE Prometheus text. Exactly one stream per (pod, metric)
#                      so obsd's Materialize binds (a labelled series splits into many
#                      streams and silently fails to bind).
#   * GET /ctl?...  -> deterministic, repeatable failure injection (the control surface).
#   * GET /healthz  -> readiness.
#
# The data path is REAL (real Mosquitto/InfluxDB/MinIO/Postgres). Backend calls are
# best-effort + reconnecting: when a dependency is down (during a chaos run), the pod
# stays Ready and keeps exporting metrics — Vigil still sees it, and the staleness/queue
# signals are exactly what reveal the failure. The ONLY role that intentionally dies is
# the analyzer under /ctl?leak=on (the MEMORY_LEAK -> OOM_KILL_CGROUP flagship).

import http.server
import io
import json
import os
import socketserver
import threading
import time
import urllib.parse
import urllib.request

ROLE = os.environ.get("ROLE", "sensors").strip().lower()
PORT = int(os.environ.get("PORT", "8080"))
SITE = os.environ.get("SITE_ID", "WTP_NORDIC_01")
AREA = os.environ.get("AREA_ID", "pumping")

# --- Backend coordinates (Service DNS, injected via env / genix-credentials secret) ---
SENSORS_URL = os.environ.get("SENSORS_URL", "http://smart-sensors:8080")
BROKER_HOST = os.environ.get("BROKER_HOST", "edgenius-broker")
BROKER_PORT = int(os.environ.get("BROKER_PORT", "1883"))
INFLUX_URL = os.environ.get("INFLUX_URL", "http://genix-historian:8086")
INFLUX_TOKEN = os.environ.get("INFLUX_TOKEN", "genix-token")
INFLUX_ORG = os.environ.get("INFLUX_ORG", "abb")
INFLUX_BUCKET = os.environ.get("INFLUX_BUCKET", "telemetry")
PG_HOST = os.environ.get("PG_HOST", "asset-registry")
PG_USER = os.environ.get("PG_USER", "genix")
PG_PASSWORD = os.environ.get("PG_PASSWORD", "genix")
PG_DB = os.environ.get("PG_DB", "assets")
DATALAKE_ENDPOINT = os.environ.get("DATALAKE_ENDPOINT", "genix-datalake:9000")
DATALAKE_BUCKET = os.environ.get("DATALAKE_BUCKET", "telemetry-archive")
MINIO_USER = os.environ.get("MINIO_ROOT_USER", "genix")
MINIO_PASSWORD = os.environ.get("MINIO_ROOT_PASSWORD", "genix-password")
TOPIC_ROOT = os.environ.get("TOPIC_ROOT", "abb/genix")

# Plant asset population (the ABB Smart Sensor fleet). Overridable via PLANT_ASSETS
# (comma-separated) so the plant-topology ConfigMap can reshape the simulation.
DEFAULT_ASSETS = "raw-water-pump-1,raw-water-pump-2,booster-pump-1,backwash-blower-1,sludge-mixer-motor-1"
ASSETS = [a.strip() for a in os.environ.get("PLANT_ASSETS", DEFAULT_ASSETS).split(",") if a.strip()]

# Shared, lock-guarded control + counter state. All metrics are exported label-free.
S = {
    "queue": 0.0,            # stream: pending MQTT messages awaiting historian write (L4 bar)
    "requests": 0.0,         # gateway/assetapi: cumulative request/sample counter (rate -> L1 bar)
    "writes": 0.0,           # stream: cumulative historian writes
    "datalake_objects": 0.0, # stream: cumulative batches archived to the data lake (MinIO)
    "alerts": 0.0,           # analyzer: cumulative PdM alerts published
    "inference_seconds": 0.0,# analyzer: last model inference duration
    "broker_connected": 0.0, # gateway/stream: 1 if MQTT session up
    "last_update": time.time(),  # freshness gauge (Unix epoch) — freezes when upstream stalls
    "assets": float(len(ASSETS)),
    "sample_rate": float(os.environ.get("SAMPLE_RATE", "5")),  # samples/sec the gateway forwards
    "slow_ms": 0.0,          # stream: injected per-message processing latency
    "disconnected": False,   # gateway: connectivity-loss injection
    "leak": False,           # analyzer: memory-leak injection
}
LOCK = threading.Lock()
_LEAK_BUF = []  # analyzer leak sink — grows until the cgroup OOMs at the declared limit


def log(msg):
    print(f"[{ROLE}] {msg}", flush=True)


# --------------------------------------------------------------------------- #
# Lazy, guarded backend clients. A missing lib or a down dependency never
# crashes the pod — it degrades to "no fresh data", which is the signal.
# --------------------------------------------------------------------------- #
def mqtt_client(client_id):
    try:
        import paho.mqtt.client as mqtt
    except Exception as e:  # noqa: BLE001
        log(f"paho-mqtt unavailable: {e}")
        return None
    c = mqtt.Client(client_id=client_id, clean_session=True)
    c.reconnect_delay_set(min_delay=1, max_delay=8)
    return c


def influx_writer():
    try:
        from influxdb_client import InfluxDBClient, Point
        from influxdb_client.client.write_api import SYNCHRONOUS
    except Exception as e:  # noqa: BLE001
        log(f"influxdb-client unavailable: {e}")
        return None, None, None
    client = InfluxDBClient(url=INFLUX_URL, token=INFLUX_TOKEN, org=INFLUX_ORG)
    return client, client.write_api(write_options=SYNCHRONOUS), Point


def influx_query_api():
    try:
        from influxdb_client import InfluxDBClient
    except Exception as e:  # noqa: BLE001
        log(f"influxdb-client unavailable: {e}")
        return None
    return InfluxDBClient(url=INFLUX_URL, token=INFLUX_TOKEN, org=INFLUX_ORG)


def pg_connect():
    try:
        import psycopg2
    except Exception as e:  # noqa: BLE001
        log(f"psycopg2 unavailable: {e}")
        return None
    try:
        conn = psycopg2.connect(
            host=PG_HOST, user=PG_USER, password=PG_PASSWORD, dbname=PG_DB, connect_timeout=5
        )
        conn.autocommit = True
        return conn
    except Exception as e:  # noqa: BLE001
        log(f"postgres connect failed: {e}")
        return None


def minio_client():
    try:
        from minio import Minio
    except Exception as e:  # noqa: BLE001
        log(f"minio unavailable: {e}")
        return None
    try:
        return Minio(DATALAKE_ENDPOINT, access_key=MINIO_USER, secret_key=MINIO_PASSWORD, secure=False)
    except Exception as e:  # noqa: BLE001
        log(f"minio client init failed: {e}")
        return None


# --------------------------------------------------------------------------- #
# ROLE: sensors — the ABB Ability Smart Sensor fleet (L1 field).
# Generates synthetic vibration / bearing-temp / motor-current / speed per asset.
# A degrading asset ramps its vibration so the analyzer has something to catch.
# --------------------------------------------------------------------------- #
def role_sensors():
    import math
    import random

    state = {}
    for a in ASSETS:
        state[a] = {"vib": 0.5 + random.random() * 0.2, "degrade": False, "phase": random.random() * 6.28}

    def loop():
        t = 0
        while True:
            with LOCK:
                degraded_set = {a for a in ASSETS if state[a]["degrade"]}
            for a in ASSETS:
                st = state[a]
                base = 0.5 + 0.1 * math.sin(t / 12.0 + st["phase"])
                if st["degrade"]:
                    st["vib"] = min(8.0, st["vib"] + 0.05)  # bearing spalling: vibration climbs
                else:
                    st["vib"] = base + random.uniform(-0.05, 0.05)
            with LOCK:
                S["last_update"] = time.time()
            t += 1
            time.sleep(1.0)

    threading.Thread(target=loop, daemon=True).start()

    def reading(a):
        st = state[a]
        vib = st["vib"]
        return {
            "asset": a, "site": SITE, "area": AREA,
            "vibration_g_rms": round(vib, 4),
            "bearing_temp_c": round(45.0 + vib * 6.0, 2),     # temp tracks vibration
            "motor_current_a": round(18.0 + vib * 1.5, 2),
            "speed_rpm": round(1480 - vib * 10, 1),
            "ts": time.time(),
        }

    def telemetry():
        return [reading(a) for a in ASSETS]

    def ctl(qs):
        with LOCK:
            if "rate" in qs:
                S["sample_rate"] = float(qs["rate"][0])
            if "degrade" in qs:
                tgt = qs["degrade"][0]
                for a in ASSETS:
                    if a == tgt or tgt in ("all", "1", "on"):
                        state[a]["degrade"] = True
            if "heal" in qs:
                for a in ASSETS:
                    state[a]["degrade"] = False
                    state[a]["vib"] = 0.5

    extra_routes = {"/telemetry": lambda: (200, json.dumps(telemetry()))}
    return extra_routes, ctl


# --------------------------------------------------------------------------- #
# ROLE: gateway — System 800xA / OPC-UA connectivity server (L2).
# Polls smart-sensors, publishes each reading to the Edgenius MQTT broker.
# /ctl?disconnect=1 simulates field connectivity loss (the cascade source).
# --------------------------------------------------------------------------- #
def role_gateway():
    client = mqtt_client(f"opcua-gateway-{os.environ.get('HOSTNAME','x')}")
    connected = {"v": False}

    if client is not None:
        def on_connect(c, u, f, rc):
            connected["v"] = rc == 0
            log(f"broker connect rc={rc}")

        def on_disconnect(c, u, rc):
            connected["v"] = False

        client.on_connect = on_connect
        client.on_disconnect = on_disconnect
        try:
            client.connect_async(BROKER_HOST, BROKER_PORT, keepalive=30)
            client.loop_start()
        except Exception as e:  # noqa: BLE001
            log(f"broker connect_async failed: {e}")

    def poll_loop():
        while True:
            with LOCK:
                rate = max(0.2, S["sample_rate"])
                down = S["disconnected"]
            interval = 1.0 / rate
            if down:
                with LOCK:
                    S["broker_connected"] = 0.0
                time.sleep(0.5)
                continue
            try:
                with urllib.request.urlopen(f"{SENSORS_URL}/telemetry", timeout=4) as r:
                    readings = json.loads(r.read().decode())
            except Exception:  # noqa: BLE001  (sensors unreachable: forward nothing)
                readings = []
            published = 0
            for rd in readings:
                topic = f"{TOPIC_ROOT}/{rd['site']}/{rd['area']}/{rd['asset']}"
                if client is not None and connected["v"]:
                    try:
                        client.publish(topic, json.dumps(rd), qos=0)
                        published += 1
                    except Exception:  # noqa: BLE001
                        pass
            with LOCK:
                # L1 LOAD_SURGE measures the FIELD/forward rate, which is broker-independent:
                # count every reading polled+forwarded, not only successful broker publishes
                # (else a flapping/cold broker silently freezes the counter -> false-negative surge).
                S["requests"] += len(readings)   # rate(app_requests_total) -> L1 LOAD_SURGE bar
                S["broker_connected"] = 1.0 if connected["v"] else 0.0
                if readings:
                    S["last_update"] = time.time()
            time.sleep(interval)

    threading.Thread(target=poll_loop, daemon=True).start()

    def ctl(qs):
        with LOCK:
            if "rate" in qs:
                S["sample_rate"] = float(qs["rate"][0])
            if "disconnect" in qs:
                S["disconnected"] = qs["disconnect"][0] in ("1", "true", "on", "yes")
    return {}, ctl


# --------------------------------------------------------------------------- #
# ROLE: stream — Genix Integrate normalization (L3 ingest).
# Subscribes to the broker, queues messages, drains them into the historian.
# Queue depth grows under throttle/burst -> L4 QUEUE_SATURATION bar.
# --------------------------------------------------------------------------- #
def role_stream():
    import collections

    q = collections.deque(maxlen=100000)   # bounded: a long backpressure rig can't OOM the pod
    qlock = threading.Lock()
    client = mqtt_client(f"stream-processor-{os.environ.get('HOSTNAME','x')}")
    influx, write_api, Point = influx_writer()
    mc = minio_client()
    batch = []                 # readings awaiting data-lake archival
    batch_lock = threading.Lock()
    burn = {"on": False}       # CPU-burn injection (genuine throttling) — /ctl?cpuburn=1

    # Telemetry topic is TOPIC_ROOT/<site>/<area>/<asset> (3 levels); the analyzer's
    # alerts live at TOPIC_ROOT/alerts/<asset> (2 levels). Subscribe to the telemetry
    # subtree ONLY so alerts never re-enter the queue and inflate the L4 signal.
    TELEMETRY_SUB = f"{TOPIC_ROOT}/+/+/+"

    if client is not None:
        def on_connect(c, u, f, rc):
            with LOCK:
                S["broker_connected"] = 1.0 if rc == 0 else 0.0
            if rc == 0:
                c.subscribe(TELEMETRY_SUB, qos=0)
            log(f"broker connect rc={rc}, subscribed {TELEMETRY_SUB}")

        def on_message(c, u, msg):
            try:
                rd = json.loads(msg.payload.decode())
            except Exception:  # noqa: BLE001
                return
            if "vibration_g_rms" not in rd:   # defensive: only telemetry readings are queued
                return
            with qlock:
                q.append(rd)
            with LOCK:
                S["queue"] = float(len(q))

        client.on_connect = on_connect
        client.on_message = on_message
        try:
            client.connect_async(BROKER_HOST, BROKER_PORT, keepalive=30)
            client.loop_start()
        except Exception as e:  # noqa: BLE001
            log(f"broker connect_async failed: {e}")

    def drain_loop():
        while True:
            with qlock:
                rd = q.popleft() if q else None
                depth = len(q)
            with LOCK:
                S["queue"] = float(depth)
                slow = S["slow_ms"]
            if rd is None:
                time.sleep(0.2)
                continue
            if slow > 0:
                time.sleep(slow / 1000.0)  # injected processing latency -> queue builds
            if write_api is not None and Point is not None:
                try:
                    p = (
                        Point("asset_telemetry")
                        .tag("asset", rd["asset"]).tag("site", rd["site"]).tag("area", rd["area"])
                        .field("vibration_g_rms", float(rd["vibration_g_rms"]))
                        .field("bearing_temp_c", float(rd["bearing_temp_c"]))
                        .field("motor_current_a", float(rd["motor_current_a"]))
                        .field("speed_rpm", float(rd["speed_rpm"]))
                    )
                    write_api.write(bucket=INFLUX_BUCKET, org=INFLUX_ORG, record=p)
                    with LOCK:
                        S["writes"] += 1
                        S["last_update"] = time.time()
                    with batch_lock:
                        batch.append(rd)
                        if len(batch) > 5000:       # bounded: drop oldest if the lake is unreachable
                            del batch[:1000]
                except Exception:  # noqa: BLE001  (historian down: drop, queue keeps draining)
                    pass

    def flush_loop():
        # Periodic data-lake archival: NDJSON batches to the Genix data lake (MinIO).
        # Makes genix-datalake a REAL downstream dependency, not a decoration.
        ensured = {"v": False}
        while True:
            time.sleep(float(os.environ.get("FLUSH_INTERVAL", "30")))
            with batch_lock:
                if not batch:
                    continue
                payload = "\n".join(json.dumps(r) for r in batch).encode()
                n = len(batch)
                batch.clear()
            if mc is None:
                continue
            try:
                if not ensured["v"]:
                    if not mc.bucket_exists(DATALAKE_BUCKET):
                        mc.make_bucket(DATALAKE_BUCKET)
                    ensured["v"] = True
                obj = f"{SITE}/{AREA}/batch-{int(time.time())}.ndjson"
                mc.put_object(DATALAKE_BUCKET, obj, io.BytesIO(payload), length=len(payload),
                              content_type="application/x-ndjson")
                with LOCK:
                    S["datalake_objects"] += 1
                log(f"archived {n} readings -> s3://{DATALAKE_BUCKET}/{obj}")
            except Exception as e:  # noqa: BLE001  (lake down: skip this batch)
                log(f"datalake flush failed: {e}")

    def burn_loop():
        # Busy spin when armed -> burns CPU past the container's CPU limit -> CFS
        # throttling (the THROTTLING_CASCADE signal). Idle (sleeping) otherwise.
        x = 0
        while True:
            if burn["on"]:
                x = (x * 1103515245 + 12345) & 0x7FFFFFFF   # cheap busy work
            else:
                time.sleep(0.2)

    # A few drainers so normal load keeps the queue near zero; throttle/burst overwhelms them.
    for _ in range(int(os.environ.get("DRAINERS", "2"))):
        threading.Thread(target=drain_loop, daemon=True).start()
    threading.Thread(target=flush_loop, daemon=True).start()
    for _ in range(int(os.environ.get("BURN_THREADS", "2"))):
        threading.Thread(target=burn_loop, daemon=True).start()

    def ctl(qs):
        with LOCK:
            if "slow" in qs:
                S["slow_ms"] = float(qs["slow"][0])
        if "cpuburn" in qs:                      # genuine CPU pressure -> THROTTLING_CASCADE
            burn["on"] = qs["cpuburn"][0] in ("1", "true", "on", "yes")
    return {}, ctl


# --------------------------------------------------------------------------- #
# ROLE: analyzer — Genix Model Fabric predictive maintenance (L3 analytics).
# Queries recent vibration, computes a simple trend -> remaining-useful-life,
# publishes alerts to the broker, upserts findings into Postgres.
# /ctl?leak=on -> the MEMORY_LEAK -> OOM_KILL_CGROUP flagship.
# --------------------------------------------------------------------------- #
def role_analyzer():
    client = mqtt_client(f"pdm-analyzer-{os.environ.get('HOSTNAME','x')}")
    if client is not None:
        try:
            client.connect_async(BROKER_HOST, BROKER_PORT, keepalive=30)
            client.loop_start()
        except Exception as e:  # noqa: BLE001
            log(f"broker connect_async failed: {e}")

    iq = influx_query_api()

    def ensure_schema(conn):
        try:
            with conn.cursor() as cur:
                cur.execute(
                    "CREATE TABLE IF NOT EXISTS asset_health ("
                    "asset TEXT PRIMARY KEY, vibration DOUBLE PRECISION, rul_hours DOUBLE PRECISION, "
                    "severity TEXT, updated_at TIMESTAMPTZ DEFAULT now())"
                )
        except Exception as e:  # noqa: BLE001
            log(f"schema init failed: {e}")

    def latest_vibration():
        # Best-effort: last vibration per asset over the recent window.
        out = {}
        if iq is None:
            return out
        try:
            flux = (
                f'from(bucket:"{INFLUX_BUCKET}") |> range(start:-10m) '
                f'|> filter(fn:(r)=>r._measurement=="asset_telemetry" and r._field=="vibration_g_rms") '
                f'|> last()'
            )
            for table in iq.query_api().query(flux, org=INFLUX_ORG):
                for rec in table.records:
                    out[rec.values.get("asset")] = rec.get_value()
        except Exception:  # noqa: BLE001
            pass
        return out

    def analyze_loop():
        conn = pg_connect()
        if conn is not None:
            ensure_schema(conn)
        while True:
            # Recover from a startup DNS race or a dropped connection: if we have no
            # Postgres handle, retry every cycle (the once-at-start connect is not enough —
            # asset-registry DNS may not resolve yet when the analyzer first boots).
            if conn is None:
                conn = pg_connect()
                if conn is not None:
                    ensure_schema(conn)
            t0 = time.time()
            vib = latest_vibration()
            for asset, v in vib.items():
                # Trivial RUL heuristic: higher vibration -> fewer hours left.
                rul = max(0.0, (4.5 - float(v)) * 240.0)
                sev = "critical" if v >= 4.0 else "warning" if v >= 2.0 else "ok"
                if sev != "ok" and client is not None:
                    try:
                        client.publish(
                            f"{TOPIC_ROOT}/alerts/{asset}",
                            json.dumps({"asset": asset, "vibration": v, "rul_hours": rul, "severity": sev}),
                            qos=0,
                        )
                        with LOCK:
                            S["alerts"] += 1
                    except Exception:  # noqa: BLE001
                        pass
                if conn is not None:
                    try:
                        with conn.cursor() as cur:
                            cur.execute(
                                "INSERT INTO asset_health(asset,vibration,rul_hours,severity,updated_at) "
                                "VALUES(%s,%s,%s,%s,now()) ON CONFLICT(asset) DO UPDATE SET "
                                "vibration=EXCLUDED.vibration, rul_hours=EXCLUDED.rul_hours, "
                                "severity=EXCLUDED.severity, updated_at=now()",
                                (asset, float(v), rul, sev),
                            )
                    except Exception:  # noqa: BLE001
                        conn = pg_connect()  # reconnect on next pass
            with LOCK:
                S["inference_seconds"] = time.time() - t0
                S["last_update"] = time.time()
                leaking = S["leak"]
            if leaking:
                # ~4MiB / pass: working set climbs through the at-threshold band of the
                # 0.95 x limit bar (MEMORY_LEAK signature) before the kernel OOM-kills it.
                _LEAK_BUF.append(bytearray(4 * 1024 * 1024))
            time.sleep(float(os.environ.get("ANALYZE_INTERVAL", "10")))

    threading.Thread(target=analyze_loop, daemon=True).start()

    def ctl(qs):
        with LOCK:
            if "leak" in qs:
                S["leak"] = qs["leak"][0] in ("1", "true", "on", "yes")
                if not S["leak"]:
                    _LEAK_BUF.clear()
    return {}, ctl


# --------------------------------------------------------------------------- #
# ROLE: assetapi — Genix Asset Performance Management API (L3 serving).
# Serves asset health from Postgres + freshest historian reading. Its freshness
# gauge freezes when the pipeline stalls -> L6 DATA_STALENESS on this CEI.
# --------------------------------------------------------------------------- #
def role_assetapi():
    iq = influx_query_api()
    health = {"rows": [], "fresh_ts": time.time()}

    def refresh_loop():
        while True:
            rows = []
            conn = pg_connect()
            if conn is not None:
                try:
                    with conn.cursor() as cur:
                        cur.execute(
                            "SELECT asset, vibration, rul_hours, severity, "
                            "extract(epoch from updated_at) FROM asset_health"
                        )
                        # extract(epoch ...) returns a Decimal via psycopg2 — coerce all
                        # numerics to float so json.dumps in the /assets handler can't throw.
                        rows = [
                            {"asset": r[0],
                             "vibration": float(r[1]) if r[1] is not None else None,
                             "rul_hours": float(r[2]) if r[2] is not None else None,
                             "severity": r[3],
                             "updated_at": float(r[4]) if r[4] is not None else None}
                            for r in cur.fetchall()
                        ]
                    conn.close()
                except Exception:  # noqa: BLE001
                    pass
            # Freshness == newest reading we can actually serve. When the historian
            # stops advancing (connectivity loss / stream stall), this freezes and
            # age = evalNow - last_update grows past the SLO bar.
            fresh = None
            if iq is not None:
                try:
                    flux = (
                        f'from(bucket:"{INFLUX_BUCKET}") |> range(start:-30m) '
                        f'|> filter(fn:(r)=>r._measurement=="asset_telemetry") |> last()'
                    )
                    for table in iq.query_api().query(flux, org=INFLUX_ORG):
                        for rec in table.records:
                            ts = rec.get_time().timestamp()
                            fresh = ts if fresh is None else max(fresh, ts)
                except Exception:  # noqa: BLE001
                    pass
            health["rows"] = rows
            with LOCK:
                S["requests"] += 1            # each refresh is a unit of served work (counter advances)
                if fresh is not None:
                    health["fresh_ts"] = fresh
                    S["last_update"] = fresh
            time.sleep(5.0)

    threading.Thread(target=refresh_loop, daemon=True).start()

    def assets():
        with LOCK:
            S["requests"] += 1
        return 200, json.dumps({"site": SITE, "assets": health["rows"]})

    return {"/assets": assets, "/api/assets": assets}, (lambda qs: None)


# --------------------------------------------------------------------------- #
# Shared HTTP server: /metrics, /ctl, /healthz + role extra routes.
# --------------------------------------------------------------------------- #
def build_metrics():
    with LOCK:
        s = dict(S)
    lines = [
        "# HELP abb_pipeline_info Static 1 (one stream per pod).",
        "# TYPE abb_pipeline_info gauge", "abb_pipeline_info 1",
        "# HELP app_queue_depth Pending telemetry messages awaiting historian write.",
        "# TYPE app_queue_depth gauge", f"app_queue_depth {s['queue']}",
        "# HELP app_requests_total Cumulative samples forwarded / API requests served.",
        "# TYPE app_requests_total counter", f"app_requests_total {s['requests']}",
        "# HELP app_last_update_seconds Unix epoch of the freshest data this pod produced/served.",
        "# TYPE app_last_update_seconds gauge", f"app_last_update_seconds {s['last_update']}",
        "# HELP abb_historian_writes_total Cumulative points written to the historian.",
        "# TYPE abb_historian_writes_total counter", f"abb_historian_writes_total {s['writes']}",
        "# HELP abb_datalake_objects_total Cumulative telemetry batches archived to the data lake.",
        "# TYPE abb_datalake_objects_total counter", f"abb_datalake_objects_total {s['datalake_objects']}",
        "# HELP abb_pdm_alerts_total Cumulative predictive-maintenance alerts published.",
        "# TYPE abb_pdm_alerts_total counter", f"abb_pdm_alerts_total {s['alerts']}",
        "# HELP abb_pdm_inference_seconds Last model inference duration.",
        "# TYPE abb_pdm_inference_seconds gauge", f"abb_pdm_inference_seconds {s['inference_seconds']}",
        "# HELP abb_broker_connected 1 when the MQTT session is up.",
        "# TYPE abb_broker_connected gauge", f"abb_broker_connected {s['broker_connected']}",
        "# HELP abb_assets_total Assets in the simulated plant.",
        "# TYPE abb_assets_total gauge", f"abb_assets_total {s['assets']}",
    ]
    return "\n".join(lines) + "\n"


def make_handler(extra_routes, ctl):
    class H(http.server.BaseHTTPRequestHandler):
        def log_message(self, *a):  # silence access logs
            pass

        def _send(self, code, body, ctype="text/plain; version=0.0.4"):
            self.send_response(code)
            self.send_header("Content-Type", ctype)
            self.end_headers()
            self.wfile.write(body.encode() if isinstance(body, str) else body)

        def do_GET(self):
            u = urllib.parse.urlparse(self.path)
            if u.path == "/metrics":
                return self._send(200, build_metrics())
            if u.path in ("/", "/healthz", "/readyz"):
                return self._send(200, f"{ROLE} ok\n")
            if u.path == "/ctl":
                ctl(urllib.parse.parse_qs(u.query))
                with LOCK:
                    snap = {k: S[k] for k in ("queue", "requests", "sample_rate", "slow_ms",
                                              "disconnected", "leak", "broker_connected")}
                return self._send(200, f"ok {snap}\n")
            if u.path in extra_routes:
                code, body = extra_routes[u.path]()
                ctype = "application/json" if body.startswith(("{", "[")) else "text/plain"
                return self._send(code, body, ctype)
            return self._send(404, "not found\n")

    return H


def main():
    roles = {
        "sensors": role_sensors,
        "gateway": role_gateway,
        "stream": role_stream,
        "analyzer": role_analyzer,
        "assetapi": role_assetapi,
    }
    if ROLE not in roles:
        raise SystemExit(f"unknown ROLE={ROLE!r}; expected one of {sorted(roles)}")
    log(f"starting role={ROLE} site={SITE} assets={len(ASSETS)} port={PORT}")
    extra_routes, ctl = roles[ROLE]()

    class TS(socketserver.ThreadingMixIn, http.server.HTTPServer):
        daemon_threads = True
        allow_reuse_address = True

    TS(("0.0.0.0", PORT), make_handler(extra_routes, ctl)).serve_forever()


if __name__ == "__main__":
    main()
