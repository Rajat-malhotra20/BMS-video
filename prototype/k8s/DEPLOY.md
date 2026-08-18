# Deploy / Redeploy Runbook — bms-dev

Single container (MediaMTX + Go API via supervisord). Single node.
The Deployment uses `imagePullPolicy: Always` and pulls from ECR — the
cluster needs registry auth, either a Portainer ECR registry (auto-
refreshes the token) or the `ecr-pull` secret (manual ~12h refresh,
created out-of-band, not in `portainer-stack.yaml`).

Image: `475560691356.dkr.ecr.ap-south-1.amazonaws.com/bms/video/dev:latest`

The manual save/copy/load steps below are an **alternative** for a node
with no registry access at all — if you use them, also flip
`imagePullPolicy` to `Never` and drop `imagePullSecrets` in the Deployment,
or the pod will just try (and fail) to pull from ECR anyway.

---

## First deploy

### 1. Build the image (on the machine that has Docker + this repo)

From `prototype/`:
```bash
docker build -t 475560691356.dkr.ecr.ap-south-1.amazonaws.com/bms/video/dev:latest .
```

### 2. Save it to a file and copy to the node

```bash
docker save 475560691356.dkr.ecr.ap-south-1.amazonaws.com/bms/video/dev:latest -o bms.tar
scp bms.tar user@<node-ip>:/tmp/
```

If `docker save` produces something the node refuses to load (attestation /
manifest-list), build a plain single-arch tar instead:
```bash
docker buildx build --platform linux/amd64 --provenance=false \
  -t 475560691356.dkr.ecr.ap-south-1.amazonaws.com/bms/video/dev:latest \
  -o type=docker,dest=bms.tar .
```

### 3. Load it into the node's runtime (run ON the node)

Check the runtime first:
```bash
sudo crictl info 2>/dev/null | grep -i runtimeName || kubectl get node -o wide
```

Then load with the matching command:
```bash
# containerd (kubeadm / most clusters) — the k8s.io namespace is required
sudo ctr -n k8s.io images import /tmp/bms.tar

# k3s
sudo k3s ctr images import /tmp/bms.tar

# docker / cri-dockerd
sudo docker load -i /tmp/bms.tar
```

Confirm kubelet can see it:
```bash
sudo crictl images | grep bms
```

### 4. Apply the stack

```bash
kubectl apply -f portainer-stack.yaml     # or paste it into Portainer
kubectl -n bms-dev get pods -w
```

Verify:
```bash
curl http://<node-ip>:30080/api/fleet     # expect {"buses":[],...}
kubectl -n bms-dev logs deploy/bms-video         # both mediamtx + backend lines
```

---

## Redeploy a new image build

`:latest` never pulls (`imagePullPolicy: Never`), so you must reload the
tar onto the node, then roll the pod.

```bash
# 1. Rebuild + save + copy (steps 1–2 above)
# 2. Reload on the node
sudo ctr -n k8s.io images import /tmp/bms.tar    # or k3s / docker variant
# 3. Roll the pod so it picks up the new image
kubectl -n bms-dev rollout restart deploy/bms-video
kubectl -n bms-dev rollout status deploy/bms-video
```

---

## Config change only (mediamtx.yml, vendors.json, buses.json)

Edit the relevant ConfigMap block in `portainer-stack.yaml`
(`mediamtx-config` or `vendors-config`), then:
```bash
kubectl apply -f portainer-stack.yaml
kubectl -n bms-dev rollout restart deploy/bms-video   # mounted config needs a pod restart
```

---

## First-time setup: vendor bridge credentials

`POST /api/bridge/start` (the vendor-less bridge — see `HOW_TO_USE.md` §6)
needs vendor account passwords, which — same reasoning as `ecr-pull` —
must never live in a file this stack `kubectl apply`'s repeatedly. Create
the Secret once, out-of-band, **before** the first `kubectl apply`:

```bash
kubectl -n bms-dev create secret generic vendor-credentials \
  --from-literal=castmaster-password='...' \
  --from-literal=sumithlive-password='...' \
  --from-literal=chemitoapi-password='...'
```

All three keys must exist (blank string is fine for any vendor you're not
using) — the Deployment's `secretKeyRef`s fail the pod otherwise. Non-secret
fields (`baseUrl`, `username`, `extra`) and the bus→vendor map live in the
`vendors-config` ConfigMap in `portainer-stack.yaml` instead — edit that
file directly for those (see the "Config change only" section above).

To rotate a password later:
```bash
kubectl -n bms-dev create secret generic vendor-credentials \
  --from-literal=castmaster-password='NEW' \
  --from-literal=sumithlive-password='...' \
  --from-literal=chemitoapi-password='...' \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl -n bms-dev rollout restart deploy/bms-video
```

---

## Access

| Purpose | Address |
|---|---|
| JSON API (the one API) | `http://<node-ip>:30080/api/fleet` |
| RTMP publish (buses) | `rtmp://<node-ip>:31935/<bus_id>_<cam_no>` |
| WebRTC media | UDP `30189` on `bms-media.gna.energy` |
| N9M device signaling | `<node-ip>:30500` (device config points here) |
| N9M device media | `<node-ip>:30501` |

Node security group / firewall must allow inbound: `30080/TCP`, `31935/TCP`, `30189/UDP`, `30500/TCP`, `30501/TCP`.

## Preconditions to check once
- `kubectl get storageclass` — a default StorageClass must exist, else `recordings-pvc` stays Pending.
- DNS `A` record `bms-media.gna.energy` → node public IP (WebRTC ICE advertises this host).
- `vendor-credentials` Secret exists (see "First-time setup" above) — without it the pod won't start at all (missing `secretKeyRef`).
- If pod shows `ErrImageNeverPull`: only applies if you switched to the manual-load path above; otherwise check `ecr-pull` / registry auth instead.
