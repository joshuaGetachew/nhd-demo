# DevOps Interview Prep — Lab Notes

Goal: local Kubernetes cluster → small Go app → deploy as a pod → Prometheus + Grafana monitoring.

## Plan

- [x] **Step 1** — Verify local Kubernetes cluster (Rancher Desktop)
- [x] **Step 2** — Write a small Go web app with a `/metrics` endpoint
- [x] **Step 3** — Containerize it (multi-stage Dockerfile)
- [x] **Step 4** — Deploy to Kubernetes (Deployment + Service)
- [ ] **Step 5** — Install Prometheus + Grafana (Helm, kube-prometheus-stack)
- [ ] **Step 6** — Scrape the app's metrics and build a Grafana dashboard

---

## Step 1 — Local cluster (2026-10-28)

### What we did
Rancher Desktop was already installed and running. Checked tools and cluster health:

```sh
kubectl config current-context   # -> rancher-desktop
kubectl get nodes                 # -> 1 node, Ready, v1.36.5+k3s1
kubectl get pods -A               # all kube-system pods Running/Completed
docker context ls                 # -> rancher-desktop (moby engine)
```

### Environment
| Tool | Status |
|---|---|
| rdctl, kubectl, docker, nerdctl, helm | Installed by Rancher Desktop in `~/.rd/bin` |
| Kubernetes | k3s v1.36.5, single node `lima-rancher-desktop` |
| Container engine | **moby** (dockerd) |
| Go | **Not installed** on the Mac (installed in Step 2) |

### Concepts to know
- **Rancher Desktop** runs a small Linux VM (via Lima) on the Mac. Inside it run **k3s** (a lightweight Kubernetes distribution) and a container engine.
- **kubectl context**: which cluster `kubectl` talks to. Ours is `rancher-desktop`.
- **kube-system pods** we saw:
  - `coredns`: DNS inside the cluster (lets pods reach services by name)
  - `traefik`: ingress controller, routes HTTP from outside to services
  - `local-path-provisioner`: creates storage volumes on the node's disk
  - `metrics-server`: CPU/memory stats for `kubectl top` (not the same as Prometheus)
  - `helm-install-*`: one-off Jobs k3s uses to install Traefik. `Completed` is the expected status.
- **moby engine matters**: images built with `docker build` go into the same image store k3s uses, so the cluster can run a locally built image **without pushing it to a registry**. (With the containerd engine you'd use `nerdctl --namespace k8s.io build` instead.)

### What broke / observations
- `helm-install-traefik` shows `2 restarts`. That's normal on first boot: the Job retries until the Traefik CRDs exist. Nothing to fix.
- Go isn't installed. **Decision:** don't install it yet. We'll compile inside a Docker multi-stage build (the `golang` image provides the toolchain), which is how CI builds it anyway. We can `brew install go` later for fast local iteration.

---

## Repo setup (2026-10-28)

### What we did
```sh
git init -b main                              # new repo, default branch "main"
git config user.name / user.email             # set for THIS repo only (no global identity existed)
echo .DS_Store > .gitignore                   # keep macOS Finder junk out of the repo
git add NOTES.md README.md .gitignore && git commit
```

### What broke / observations
- No global git identity was configured, and git refuses to commit without one. Set it per-repo with `git config` (without `--global`).
- `gh` (GitHub CLI) isn't installed, so pushing uses plain `git push` over HTTPS. GitHub needs a **personal access token** (or SSH key) for that, not your password.
- The remote URL given was a template (`YOUR-USERNAME`), so the push waits for the real username.
- Added the remote: `git remote add origin https://github.com/joshuaGetachew/nhd-demo.git`. `git ls-remote origin` came back empty with no error, which means the repo exists and has no commits yet.
- First `git push` failed: `could not read Username for 'https://github.com'`. No GitHub credentials are saved on this Mac yet, and there are no SSH keys.
  **Fix:** run `git push -u origin main` once in your own terminal and enter your GitHub username plus a **personal access token** as the password. Git's `osxkeychain` credential helper saves it in the macOS Keychain, so later pushes don't ask again.
- Making the token was confusing: the fine-grained token page showed "No repository permissions added yet".
  **Fix:** under *Repository access* choose **Only select repositories** → `nhd-demo`, then under *Permissions → Repositories* click **+ Add permissions** → **Contents** and change it from Read-only to **Read and write**. (With "Public repositories" selected, the token can only read, so repository permissions aren't offered.)
  Token named `nhd-demo-push-macbook` with an expiry date: least privilege (one repo, one permission) and it expires on its own.
- Push succeeded: `* [new branch] main -> main`, and `branch 'main' set up to track 'origin/main'`. The `-u` flag sets up that tracking, so from now on a plain `git push` / `git pull` works.

---

## Step 2 — Go app (2026-10-28)

### What we did
Changed the Step 1 decision: installed Go on the Mac so the app can be run and tested directly, without a Docker build each time. The Dockerfile in Step 3 will still compile inside the `golang` image.

```sh
brew install go                                   # go1.27.3 darwin/arm64, from homebrew/core
cd app
go mod init github.com/joshuaGetachew/nhd-demo/app
go get github.com/prometheus/client_golang/prometheus@latest   # v1.25.0
go mod tidy
go vet ./... && go test -v ./...                  # PASS
go run .                                          # listens on :8080 (override with PORT=...)
curl localhost:8080/ ; curl localhost:8080/healthz ; curl localhost:8080/metrics
```

### The app (`app/main.go`)
| Endpoint | Purpose |
|---|---|
| `/` | Returns `Hello from <hostname>`. In Kubernetes the hostname is the pod name, so you can see which replica answered. |
| `/healthz` | Returns `ok`. Will be used for liveness/readiness probes in Step 4. |
| `/metrics` | Prometheus metrics in text format. |

Custom metrics:
- `http_requests_total{path, code}`: **counter**, only goes up. In PromQL use `rate(...)` to get requests/sec.
- `http_request_duration_seconds{path}`: **histogram**, latency in buckets. Use `histogram_quantile(0.95, rate(..._bucket[5m]))` for p95.
- Plus `go_*` (goroutines, GC, memory) and `process_*` (CPU, open files) from the built-in collectors.

### Concepts to know
- **go.mod / go.sum**: `go.mod` lists the module name and dependencies; `go.sum` holds checksums so builds are reproducible and tamper-evident. Commit both.
- **Custom registry** (`prometheus.NewRegistry()`) instead of the global default: tests get a clean set of metrics, and only what we register is exposed.
- **Label cardinality**: the `path` label is a fixed string per route, not `r.URL.Path`. Otherwise every random URL someone hits (`/wp-admin`, `/x123`) creates a new time series, which can blow up Prometheus memory. Unknown paths are counted under `path="/"` with `code="404"`.
- **`ReadHeaderTimeout`** on the server: guards against slow clients holding connections open (Slowloris). `go vet`/linters flag a bare `http.ListenAndServe` for this.
- **`PORT` env var**: config from the environment (12-factor), so Kubernetes can set it without rebuilding the image.
- **`httptest.NewServer`**: the test starts a real HTTP server on a random port, so it exercises routing and the metrics output, not just handler functions.

### What broke / observations
- `brew install go` printed a **tap trust** warning about a third-party tap, `sidneys/homebrew`, that was already on this Mac. Go itself came from the official `homebrew/core`, so the warning didn't affect this install. Left the tap untrusted; decide separately whether you still need it (`brew untap sidneys/homebrew` removes it).
- First smoke test printed nothing: `curl` ran before the server had started listening. Fixed with `curl --retry 10 --retry-connrefused --retry-delay 1`. The same race is why Kubernetes has **readiness probes**: don't send traffic until the app answers.
- Added `/app/app` to `.gitignore` so a binary from `go build` never gets committed.

---

## Step 3 — Containerize (2026-10-29)

### What we did
Wrote `app/Dockerfile` (multi-stage) and `app/.dockerignore`, then built and ran the image:

```sh
cd app
docker build -t nhd-demo:0.1.0 .
docker run -d --name nhd-demo-test -p 8082:8080 nhd-demo:0.1.0
curl localhost:8082/            # Hello from 4319676c20fa  (container ID = hostname)
curl localhost:8082/healthz     # ok
curl localhost:8082/metrics | grep http_requests
docker rm -f nhd-demo-test
```

| Image | Size |
|---|---|
| `golang:1.27.3` (build stage only) | 1.32GB |
| `nhd-demo:0.1.0` (what we ship) | **18.2MB** |

### The Dockerfile, line by line
| Line | Why |
|---|---|
| `FROM golang:1.27.3 AS build` | Build stage with the full Go toolchain. Exact version pinned to match `go.mod`, so builds are reproducible. |
| `COPY go.mod go.sum ./` then `RUN go mod download` | **Layer caching**: dependencies are downloaded in their own layer, which is reused until go.mod/go.sum change. Editing `main.go` doesn't re-download anything. |
| `COPY . .` | Copies the source. `.dockerignore` keeps out local binaries and the Dockerfile itself. |
| `CGO_ENABLED=0 go build` | Produces a **static binary** with no dependency on C libraries, so it runs on an image with no libc. |
| `-trimpath -ldflags="-s -w"` | Removes local file paths and debug symbols: smaller binary, no info about the build machine. |
| `FROM gcr.io/distroless/static-debian12:nonroot` | Runtime stage. **Distroless**: no shell, no package manager, no extra tools. Only the build output is copied in. |
| `USER nonroot:nonroot` | Runs as an unprivileged user (UID 65532), not root. Limits the damage if the app is compromised. Kubernetes can enforce this with `runAsNonRoot: true`. |
| `EXPOSE 8080` | Documents the port. It doesn't publish anything; `-p` (Docker) or a Service (Kubernetes) does that. |
| `ENTRYPOINT ["/app"]` | **Exec form** (JSON array): the app runs as PID 1 and receives SIGTERM directly when Kubernetes stops the pod. The shell form would need a shell, which distroless doesn't have. |

### Concepts to know
- **Multi-stage build**: compile in a big image, copy only the result into a tiny one. The compiler, source code and caches never reach production.
- **Smaller image = smaller attack surface**: fewer packages means fewer CVEs for scanners to flag, faster pulls and faster pod startup.
- **Image tag** `0.1.0` instead of `latest`: Kubernetes knows exactly which version is running, and rollbacks are clear. With `latest`, two nodes can run different code under the same name.
- **Moby engine** (Step 1): the image is in the same store k3s uses, so Step 4 can deploy it without pushing to a registry. Use `imagePullPolicy: IfNotPresent` (or `Never`) so Kubernetes doesn't try to pull it from Docker Hub.

### What broke / observations
- `docker exec nhd-demo-test sh` fails: `exec: "sh": executable file not found`. **Expected**: distroless has no shell. That's the security point. To debug a distroless pod, use `kubectl debug` to attach a temporary debug container, or build with the `:debug` tag variant, which includes busybox.
- Image is built for **linux/arm64** (the Rancher Desktop VM on Apple Silicon). An x86 server would need `docker buildx build --platform linux/amd64`.

---

## Step 4 — Deploy to Kubernetes (2026-10-29)

### What we did
Wrote three manifests in `k8s/` and applied them:

```sh
kubectl apply -f k8s/namespace.yaml        # namespace first, so the others have somewhere to go
kubectl apply -f k8s/
kubectl -n nhd-demo rollout status deployment/nhd-demo
kubectl -n nhd-demo get pods               # 3/3 Running, 1/1 Ready
```

| File | What it creates |
|---|---|
| `namespace.yaml` | Namespace `nhd-demo`: a folder that keeps our resources separate from `kube-system`. |
| `deployment.yaml` | Deployment that keeps **3 replicas** of `nhd-demo:0.1.0` running. |
| `service.yaml` | ClusterIP Service: one stable name/IP (`nhd-demo`, port 80) that load-balances to the pods' port 8080. |

### Testing
```sh
# Load balancing, from a temporary pod inside the cluster:
kubectl -n nhd-demo run curl-test --rm -i --restart=Never --image=curlimages/curl:8.11.1 \
  --command -- sh -c 'for i in 1 2 3 4 5 6 7 8 9; do curl -s http://nhd-demo/; done'
# -> "Hello from nhd-demo-7cbb7c5f95-4w72s / -zvfzb / -hdqvm": all 3 pods answer

# From the Mac (tunnel through kubectl; Ctrl+C to stop):
kubectl -n nhd-demo port-forward svc/nhd-demo 8090:80
curl localhost:8090/

# Self-healing:
kubectl -n nhd-demo delete pod <one-pod-name>
kubectl -n nhd-demo get pods               # a replacement appears within a second
```

### The Deployment, key fields
| Field | Why |
|---|---|
| `replicas: 3` | Desired state. The Deployment (via a ReplicaSet) creates or deletes pods until there are exactly 3. |
| `selector` / `labels: app: nhd-demo` | How the Deployment and Service find their pods. **Labels are the glue in Kubernetes**: the Service doesn't list pods, it selects anything with `app: nhd-demo`. |
| `imagePullPolicy: Never` | Use the locally built image; don't try Docker Hub. Works because of the moby engine (Step 1). In real clusters you push to a registry and use `IfNotPresent`. |
| `ports: name: http` | Named port. The probes and the Service refer to `http` instead of repeating 8080. |
| `readinessProbe` → `/healthz` | Pod only gets traffic after it answers. Failing = removed from the Service, **not restarted**. |
| `livenessProbe` → `/healthz` | Failing repeatedly = container is **restarted**. `initialDelaySeconds` avoids killing it while starting. |
| `resources.requests` | What the scheduler reserves on a node (10m CPU = 1% of a core, 16Mi memory). |
| `resources.limits.memory` | Hard cap. Over 64Mi → container is **OOMKilled**. No CPU limit on purpose: CPU limits cause throttling; the request is enough to share CPU fairly. |
| `securityContext` | `runAsNonRoot`, numeric `runAsUser: 65532`, no privilege escalation, read-only root filesystem, all Linux capabilities dropped. Matches the "restricted" Pod Security Standard. |

### The Service
- `type: ClusterIP`: only reachable **inside** the cluster. Pods reach it by DNS name: `nhd-demo` (same namespace) or `nhd-demo.nhd-demo.svc.cluster.local` (full name, from anywhere). CoreDNS from Step 1 does this.
- `port: 80` → `targetPort: http` (8080): clients use 80, the Service forwards to the container port.
- Other types: **NodePort** (opens a port on every node), **LoadBalancer** (cloud load balancer; on k3s, the built-in ServiceLB). For HTTP from outside, you'd normally use an **Ingress** through Traefik.
- `kubectl get endpointslices` shows which pod IPs the Service currently sends to. Only **Ready** pods are listed.

### What broke / observations
- **`CreateContainerConfigError`** on all 3 pods. `kubectl describe pod` → Events said:
  `container has runAsNonRoot and image has non-numeric user (nonroot), cannot verify user is non-root`.
  The Dockerfile sets `USER nonroot` (a *name*). Kubernetes can only verify a *numeric* UID is non-zero.
  **Fix:** `runAsUser: 65532` / `runAsGroup: 65532` in the pod `securityContext` (65532 is distroless's nonroot UID). `kubectl apply` again did a **rolling update**: new pods came up, old ones were removed.
  **Lesson:** when a pod is stuck, `kubectl describe pod` → **Events** at the bottom is the first place to look.
- Load balancing isn't strict round-robin: kube-proxy picks a pod at random **per connection**. Over many requests it evens out.
- `port-forward` tunnels to **one** pod, so it doesn't show load balancing. That's why the load-balancing test runs from inside the cluster.
- Deleted pod showed **`Error`** while terminating. Kubernetes sends **SIGTERM** on shutdown; the app doesn't handle it, so Go exits with a non-zero code. Fix later: catch SIGTERM and call `srv.Shutdown()` to finish in-flight requests (**graceful shutdown**).
- The `curl-test` pod printed `couldn't attach to pod ... falling back to streaming logs`. It finished too fast for `kubectl` to attach; the output still came through. Harmless.

---

## Study notes: Session 1, cluster check and GitHub setup (2026-10-28)

A plain-English recap of the first session, for review before the interview.

### What we built
1. **Checked the Kubernetes cluster.** Rancher Desktop was already running a one-node cluster (k3s). All the built-in system pods were healthy, so nothing needed fixing.
2. **Started this notes file** and a plan of six steps.
3. **Turned the folder into a git repository**, added a README.md that describes the project, and a `.gitignore` so macOS's hidden `.DS_Store` files are never committed.
4. **Connected it to GitHub** at `github.com/joshuaGetachew/nhd-demo` and pushed the first commits.

### Questions I asked, and the answers

**"Connect it to `https://github.com/YOUR-USERNAME/nhd-demo.git`."**
That URL was a template. `YOUR-USERNAME` is a placeholder, so there was nothing real to push to. Claude made the commits locally and asked for my real username. It also said to create the repo on GitHub **empty** (no README ticked). If GitHub creates its own first commit, it clashes with the commits already on my Mac and the push gets rejected.

**"There are no repository permissions" (on GitHub's token page).**
The page said "No repository permissions added yet". That message doesn't mean something is wrong. It means you haven't added any yet. The fix:
- Under *Repository access*, choose **Only select repositories** and pick `nhd-demo`.
- Under *Permissions*, click **+ Add permissions**, choose **Contents**, and change it from *Read-only* to **Read and write**.
- If *Public repositories* is selected, the token can only read, so GitHub doesn't offer repository permissions at all.

**"What should I name the token?"**
The name is only a label for me. GitHub doesn't use it. A good name says what it's for and where it lives: `nhd-demo-push-macbook`. Then if I ever need to revoke it, I know which one to delete. I also set an **expiry date** instead of "No expiration".

### Where I got stuck, and how it was resolved

**The first push failed: `could not read Username for 'https://github.com'`.**
- *Why:* my Mac had no saved GitHub login, and no SSH key.
- *Why a token and not my password:* GitHub stopped accepting account passwords for git in 2021. You use a **personal access token** (or an SSH key) instead.
- *Fix:* I ran `git push -u origin main` in my own terminal. I typed my username and pasted the token as the password. Nothing shows on screen while you paste; that's normal.
- *Why it only asks once:* git's `osxkeychain` helper saves the token in the macOS Keychain, so later pushes just work.

**Git had no name or email set.**
Git won't make a commit without knowing who the author is. Claude set my name and email **for this repo only**, using `git config` without `--global`.

**Go wasn't installed.**
At first we planned to compile the app inside Docker, so no local Go was needed. In Step 2 we installed it anyway, so the app can be run and tested quickly.

**Things that looked like errors but weren't**
- The system pod `helm-install-traefik` showed *2 restarts* and a status of *Completed*. That's normal. It's a one-off setup job that retries until the things it depends on are ready, then finishes.
- My terminal prompt starts with `(base)`. That means a Python (conda) environment is active. It has nothing to do with this project.

### Ideas to be able to explain in an interview
- **What a Kubernetes context is:** which cluster `kubectl` is talking to. Mine is `rancher-desktop`.
- **What the system pods do:** CoreDNS (DNS names inside the cluster), Traefik (routes outside web traffic in), metrics-server (CPU/memory numbers for `kubectl top`), local-path-provisioner (creates disk storage for pods).
- **Why the moby engine helps locally:** images built with `docker build` land in the same place Kubernetes looks, so there's no need to push them to a registry first.
- **Least privilege:** my token can touch one repo, with one permission, and it expires on its own. If it leaks, the damage is small and temporary.
- **What `-u` does in `git push -u origin main`:** links my local `main` branch to GitHub's `main`. After that, plain `git push` and `git pull` know where to go.
- **Local commit vs push:** `git commit` saves a snapshot on my Mac only. `git push` uploads it to GitHub.

### Quick self-test
1. Why did GitHub reject my password, and what do you use instead?
2. Why should a new GitHub repo be created empty when you already have local commits?
3. What does "least privilege" mean for an access token?
4. Where does git keep my token on a Mac, so I'm not asked every time?
5. What's the difference between `git commit` and `git push`?

---

## Step 4b — Graceful shutdown + rolling update to 0.2.0 (2026-10-30)

### Why
In Step 4, deleted pods ended in **`Error`**. Kubernetes stops a pod by sending **SIGTERM**; the app didn't handle it, so Go was killed mid-request and exited non-zero. During a deploy, that can cut off requests that are in progress.

### What we changed
**`app/main.go`**: catch the signal and shut down cleanly:
```go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
go func() { srv.ListenAndServe() ... }()   // server runs in the background
<-ctx.Done()                                // main waits here until a signal arrives
srv.Shutdown(10s timeout)                   // stop accepting new connections, finish in-flight ones
```
- `signal.NotifyContext`: turns "SIGTERM/SIGINT arrived" into a context that's cancelled. SIGINT is Ctrl+C, so local runs shut down the same way.
- `ListenAndServe` moves into a **goroutine** (a lightweight background task) so `main` is free to wait for the signal.
- `http.ErrServerClosed` is what `ListenAndServe` returns after `Shutdown`. It's expected, so it isn't treated as an error.
- The **10s timeout** must be shorter than the pod's `terminationGracePeriodSeconds` (default **30s**). After that, Kubernetes sends **SIGKILL**, which can't be caught.

**`k8s/deployment.yaml`**: image `0.2.0`, plus:
```yaml
lifecycle:
  preStop:
    sleep:
      seconds: 5
```
**Why the pause:** on delete, Kubernetes *at the same time* (1) starts terminating the pod and (2) removes it from the Service's endpoints. Step 2 takes a moment to reach every node. Without the pause, the app could stop accepting connections while the Service still sends it traffic → failed requests. `preStop` runs **before** SIGTERM, so the pod keeps serving for 5s while routing catches up. Distroless has no `sleep` binary, so we use Kubernetes' built-in `sleep` action instead of `exec: ["sleep", "5"]`.

### Termination timeline (what happens on `kubectl delete pod`)
1. Pod marked **Terminating**; removed from Service endpoints (takes a moment to spread).
2. **preStop** runs: 5s pause, pod still serves requests.
3. **SIGTERM** sent → app logs `shutting down`, finishes in-flight requests, logs `stopped`, exits 0.
4. If still running after 30s total → **SIGKILL**.

### Testing
```sh
# Locally: exit code after SIGTERM
go build -o /tmp/app . && /tmp/app & P=$!; kill -TERM $P; wait $P; echo $?    # 0

# Build + roll out (new tag, so Kubernetes sees a change)
docker build -t nhd-demo:0.2.0 app/
kubectl apply -f k8s/
kubectl -n nhd-demo rollout status deployment/nhd-demo
```
| Test | Result |
|---|---|
| Delete a 0.2.0 pod | `Terminating` → **`Completed`** (was `Error`). Logs: `shutting down`, `stopped`. Took ~6s (5s preStop + shutdown). |
| `kubectl rollout restart` with continuous traffic (curl every 50ms for 40s from inside the cluster) | **670 requests, 670 × HTTP 200, 0 failures** → zero-downtime deploy. |

### Concepts to know
- **Rolling update**: changing the pod template (e.g. a new image tag) makes the Deployment create a **new ReplicaSet** (new pod-name hash: `7cbb7c5f95` → `57b8c7bd9f`), bring new pods up, wait for readiness, then remove old ones. The defaults `maxSurge: 25%`, `maxUnavailable: 25%` control the pace.
- **`kubectl rollout restart`**: restarts all pods with the same image (rolling). Handy after config changes.
- **`kubectl rollout undo deployment/nhd-demo`**: goes back to the previous ReplicaSet. **Rollback** works because old ReplicaSets are kept (`kubectl get rs`).
- **Why a new tag (`0.2.0`) and not rebuilding `0.1.0`**: if the tag doesn't change, the pod template doesn't change, so `kubectl apply` does nothing. A new tag also gives a clear history and makes rollback possible.
- **SIGTERM vs SIGKILL**: SIGTERM = "please stop" (can be caught and handled). SIGKILL = "stop now" (can't be caught). Kubernetes sends SIGTERM first, then SIGKILL after the grace period.

### What broke / observations
- The last 0.1.0 pods still ended in `Error` during the rollout: they were running the old code. Only 0.2.0 pods shut down cleanly.
- Images: `nhd-demo:0.1.0` (18.2MB) and `nhd-demo:0.2.0` (18.3MB) are both kept, so rolling back to 0.1.0 still works.

---
