# Deploy / Redeploy Runbook — bms-video (bms-dev)

Single container, just the Python API (no MediaMTX, no ffmpeg, no
supervisord — see `Prod/api.py`'s module docstring for why). Single node,
same `bms-dev` namespace, and the **same Deployment/Service names and
NodePort** the Go stack used — this replaces it, not runs alongside it.

Image: `475560691356.dkr.ecr.ap-south-1.amazonaws.com/bms/video-py/dev:latest`

---

## Cutover from the Go deployment

`Prod/k8s/portainer-stack.yaml` reuses the Go deployment's exact resource
names (`bms-video` Deployment, `backend` Service on NodePort `30080`,
`vendors-config` ConfigMap, `vendor-credentials`/`bms-api-auth` Secrets).
Because the names match, `kubectl apply` updates those objects **in place**
— it does not need a separate delete step, and nothing pointing at
`<node-ip>:30080` needs to change.

```bash
kubectl apply -f Prod/k8s/portainer-stack.yaml
kubectl -n bms-dev rollout status deploy/bms-video   # watch Go pod -> Python pod
```

Two resources from the old stack are **not** in the new manifest at all
(there is nothing for them to do — no MediaMTX, no RTMP/WebRTC/N9M) and are
left orphaned by `apply`. Remove them explicitly once you've confirmed the
Python pod is serving traffic:

```bash
kubectl -n bms-dev delete configmap mediamtx-config
kubectl -n bms-dev delete service bms-ingest
```

Before doing that, confirm nothing still depends on the ports `bms-ingest`
exposed (`31935` RTMP, `30189/udp` WebRTC, `30500`/`30501` N9M) — those
vendors (castmaster, n9m) have no Python adapter and simply stop being
servable after this cutover. If any bus still needs them, hold off and keep
running the Go pod for those buses (different Deployment name) until they're
migrated to Chemito/Sumith or otherwise handled.

**Rollback**, if the Python pod misbehaves: reapply the Go manifest,
`kubectl apply -f prototype/k8s/portainer-stack.yaml` — it will overwrite
`bms-video`/`backend`/`vendors-config` back to the Go spec the same way.
Keep that file around until you're confident in the cutover.

---

## First deploy (fresh cluster, no prior Go stack)

### 1. Build the image (on the machine that has Docker + this repo)

From the repo root (build context includes `Prod/`):
```bash
docker build -t 475560691356.dkr.ecr.ap-south-1.amazonaws.com/bms/video-py/dev:latest -f Prod/Dockerfile .
```

### 2. Save it to a file and copy to the node

```bash
docker save 475560691356.dkr.ecr.ap-south-1.amazonaws.com/bms/video-py/dev:latest -o bms-py.tar
scp bms-py.tar user@<node-ip>:/tmp/
```

### 3. Load it into the node's runtime (run ON the node)

```bash
# containerd (kubeadm / most clusters) — the k8s.io namespace is required
sudo ctr -n k8s.io images import /tmp/bms-py.tar

# k3s
sudo k3s ctr images import /tmp/bms-py.tar

# docker / cri-dockerd
sudo docker load -i /tmp/bms-py.tar
```

Confirm kubelet can see it:
```bash
sudo crictl images | grep bms/video-py
```

### 4. Apply the stack

```bash
kubectl apply -f Prod/k8s/portainer-stack.yaml     # or paste it into Portainer
kubectl -n bms-dev get pods -w
```

Verify:
```bash
curl http://<node-ip>:30080/api/fleet     # expect {"buses":[],...}
kubectl -n bms-dev logs deploy/bms-video
```

---

## Redeploy a new image build

```bash
# 1. Rebuild + save + copy (steps 1–2 above)
# 2. Reload on the node
sudo ctr -n k8s.io images import /tmp/bms-py.tar    # or k3s / docker variant
# 3. Roll the pod so it picks up the new image
kubectl -n bms-dev rollout restart deploy/bms-video
kubectl -n bms-dev rollout status deploy/bms-video
```

---

## Config change only (vendors.json, buses.json)

Edit the `vendors-config` ConfigMap block in `portainer-stack.yaml`, then:
```bash
kubectl apply -f Prod/k8s/portainer-stack.yaml
kubectl -n bms-dev rollout restart deploy/bms-video   # mounted config needs a pod restart
```

---

## Vendor bridge credentials / API token

Reused as-is from the Go deployment — same Secret names
(`vendor-credentials`, `bms-api-auth`), nothing to recreate. If migrating a
cluster that never had the Go stack, create them fresh:

```bash
kubectl -n bms-dev create secret generic vendor-credentials \
  --from-literal=sumithlive-password='...' \
  --from-literal=chemitoapi-password='...'

kubectl -n bms-dev create secret generic bms-api-auth --from-literal=token="$(openssl rand -hex 32)"
```

Keys are optional: only add the vendors you actually use. A missing key
leaves that vendor unable to authenticate but does not stop the pod —
`settings.py` reads each password with `os.environ.get` and skips an unset
one. An extra `castmaster-password` key left over in `vendor-credentials`
from the Go deployment is simply unused now; harmless to leave or remove.

Without `bms-api-auth`, the pod still starts and the API stays **open** —
the boot log says so (`WARNING: API_TOKEN is not set...`). Do not leave it
that way once the NodePort is reachable beyond a trusted network.

---

## One vendor account = one active session

Same constraint as the Go deployment — Chemito keeps a **single active
session per account**, and this process shares one login across every
camera for exactly that reason (`vendors/chemitoapi_adapter.py`'s
`_session`). Do not:

- Run a local copy of this API against the production Chemito/Sumith
  account while the pod is live — each side's logins knock out cameras the
  other is opening.
- Scale `bms-video` past one replica without giving each replica its own
  vendor account. It is `replicas: 1` / `strategy: Recreate` for this
  reason.

---

## Access

| Purpose | Address |
|---|---|
| JSON API | `http://<node-ip>:30080/api/fleet` |
| Stream a cam | `http://<node-ip>:30080/api/stream/{bus_id}?cam=1` |
| Stop a bus (free vendor capacity) | `POST http://<node-ip>:30080/api/stream/{bus_id}/stop` |

Node security group / firewall: `30080/TCP` is all this stack needs. After
the cutover, `31935/TCP`, `30189/UDP`, `30500/TCP`, `30501/TCP` (the old
`bms-ingest` ports) can be closed too — this process never receives media,
only vendor URLs it hands straight back to the frontend.

## Preconditions to check once
- `vendor-credentials` secret exists if you need the vendor bridge — the pod starts without it, but no vendor can authenticate.
- `ecr-pull-secret` is valid, or switch `imagePullPolicy` to `Never` and use the manual-load path only.
- No bus still depends on castmaster/n9m before deleting `bms-ingest` — see "Cutover" above.
