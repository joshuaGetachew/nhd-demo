# nhd-demo

A hands-on DevOps lab: a small Go web app running on a local Kubernetes cluster, monitored with Prometheus and Grafana.

## Stack

- **Kubernetes:** k3s via [Rancher Desktop](https://rancherdesktop.io/) (macOS)
- **App:** Go HTTP server exposing `/metrics` for Prometheus
- **Container build:** multi-stage Dockerfile (no local Go toolchain needed)
- **Monitoring:** Prometheus + Grafana via the `kube-prometheus-stack` Helm chart

## Progress

| Step | Status |
|---|---|
| 1. Local Kubernetes cluster | Done |
| 2. Go app with `/metrics` | Not started |
| 3. Containerize | Not started |
| 4. Deploy to Kubernetes | Not started |
| 5. Install Prometheus + Grafana | Not started |
| 6. Scrape app metrics + dashboard | Not started |

## Prerequisites

- Rancher Desktop with Kubernetes enabled and the **moby** (dockerd) container engine
- `kubectl`, `helm`, `docker` (bundled with Rancher Desktop)

Check the cluster:

```sh
kubectl config current-context   # rancher-desktop
kubectl get nodes                 # one node, Ready
```

## Notes

[NOTES.md](NOTES.md) is a step-by-step log of what was done, what broke, and how it was fixed.
