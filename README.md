# nhd-demo

A hands-on DevOps lab: a small Go web app running on a local Kubernetes cluster, monitored with Prometheus and Grafana.

## Stack

- **Kubernetes:** k3s via [Rancher Desktop](https://rancherdesktop.io/) (macOS)
- **App:** Go HTTP server exposing `/metrics` for Prometheus
- **Container build:** multi-stage Dockerfile (compiles inside the `golang` image)
- **Monitoring:** Prometheus + Grafana via the `kube-prometheus-stack` Helm chart

## Progress

| Step | Status |
|---|---|
| 1. Local Kubernetes cluster | Done |
| 2. Go app with `/metrics` | Done |
| 3. Containerize | Done |
| 4. Deploy to Kubernetes | Done |
| 5. Install Prometheus + Grafana | Not started |
| 6. Scrape app metrics + dashboard | Not started |

## Prerequisites

- Rancher Desktop with Kubernetes enabled and the **moby** (dockerd) container engine
- `kubectl`, `helm`, `docker` (bundled with Rancher Desktop)
- Go 1.27+ for running the app locally (`brew install go`)

Check the cluster:

```sh
kubectl config current-context   # rancher-desktop
kubectl get nodes                 # one node, Ready
```

## Run the app locally

```sh
cd app
go test ./...
go run .                          # http://localhost:8080 (set PORT to change)
curl localhost:8080/metrics
```

## Build the image

```sh
cd app
docker build -t nhd-demo:0.1.0 .
docker run --rm -p 8080:8080 nhd-demo:0.1.0
```

## Deploy to Kubernetes

```sh
kubectl apply -f k8s/namespace.yaml
kubectl apply -f k8s/
kubectl -n nhd-demo get pods
kubectl -n nhd-demo port-forward svc/nhd-demo 8090:80   # then: curl localhost:8090/
```

## Notes

[NOTES.md](NOTES.md) is a step-by-step log of what was done, what broke, and how it was fixed.
