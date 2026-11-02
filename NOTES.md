# DevOps Interview Prep — Lab Notes

Goal: local Kubernetes cluster → small Go app → deploy as a pod → Prometheus + Grafana monitoring.

## Plan

- [x] **Step 1** — Verify local Kubernetes cluster (Rancher Desktop)
- [x] **Step 2** — Write a small Go web app with a `/metrics` endpoint
- [x] **Step 3** — Containerize it (multi-stage Dockerfile)
- [x] **Step 4** — Deploy to Kubernetes (Deployment + Service)
- [x] **Step 5** — Install Prometheus + Grafana (Helm, kube-prometheus-stack)
- [x] **Step 6** — Scrape the app's metrics and build a Grafana dashboard

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

## Study notes: Session 2, Go app, Docker image and Kubernetes deploy (2026-10-28 to 2026-10-30)

A plain-English recap of the second session, for review before the interview.

### What we built
1. **Installed Go** on my Mac with `brew install go`, so I can run and test the app directly instead of building a Docker image every time.
2. **Wrote a small Go web app** (`app/main.go`) with three pages:
   - `/` says `Hello from <hostname>`, so I can see which machine or pod answered.
   - `/healthz` says `ok`, so Kubernetes can check the app is alive.
   - `/metrics` lists the app's numbers (request counts, response times) in the format Prometheus reads.
3. **Wrote a test** (`app/main_test.go`) that starts the app and checks all three pages. Run it with `go test ./...`.
4. **Packaged the app as a Docker image** (`app/Dockerfile`). It's built in two stages: compile in a big 1.32GB Go image, then copy only the finished program into a tiny one. The final image is **18MB**, has no shell, and doesn't run as the admin user.
5. **Deployed it to Kubernetes** (`k8s/` folder): 3 copies (pods) of the app, and one Service that spreads requests between them.
6. **Tested Kubernetes features:** load balancing (all 3 pods answered), self-healing (deleted pods were replaced in about a second), in both the terminal and Rancher Desktop.
7. **Added graceful shutdown** (version 0.2.0). When Kubernetes stops a pod, the app now finishes what it's doing before exiting. A full restart with constant traffic: **670 requests, 0 failures**.

### Questions I asked, and the answers

**"Where do I put the 'try it yourself' code?"**
In a **terminal**, not in a file. They're commands to run. In VS Code: Terminal → New Terminal (`` Ctrl+` ``).

**"Do I push before or after testing?"**
It doesn't matter technically: testing runs the app, pushing uploads the code. Good habit: **test first, then push**, so only working code goes to GitHub.

**"What do these commands do?"**
- `cd app`: move into the `app` folder (Go needs to be where `go.mod` is).
- `go run .`: compile and start the app. It keeps running until **Ctrl+C**.
- `curl localhost:8080/`: send a web request from the terminal, like a browser would, and print the reply.
- `| grep http_`: from the output, show only lines containing `http_`.

**"What are dependencies?"**
Code someone else wrote that my app needs. Ours: the Prometheus library. Go lists them in `go.mod` and stores fingerprints in `go.sum` so nobody can swap them. Fewer dependencies = smaller image, fewer security patches, less risk.

**"What's a handler?"**
The function that runs when a request hits a particular URL. It gets the **request** (`r`, what was asked) and a **response writer** (`w`, where to write the answer). The **router** (mux) decides which handler gets which URL.

**"What's curl? And do replica pods all have the same name?"**
`curl` = a command-line web request. Replica pods run the **same image** but each gets a **unique name**: `nhd-demo` (Deployment) + `7cbb7c5f95` (version) + `x2kqp` (random). Like identical twins with different names.

**"What does `listening on :8080` mean?"**
The app has started and is waiting for requests on **port 8080**. A port is like an apartment number: the computer is the building, the port says which program gets the mail. Only one program can use a port at a time.

**"Why do I need a second terminal?"**
`go run .` and `docker run` keep the first terminal busy while the app runs. The second terminal is free to send `curl` requests. (Alternative: `docker run -d` runs it in the background.)

**"Why does the container say a different 'Hello from'?"**
The code asks "what's my hostname?" each time. On my Mac the answer is my Mac's name. A container has its **own hostname** (its ID), and a Kubernetes pod's hostname is its **pod name**. Same code, different answer depending on where it runs.

**"So an image is the recipe and a container is the plate of food?"**
Close. More exactly:
| Kitchen | Docker |
|---|---|
| Recipe | **Dockerfile** |
| Cooking and freezing a meal | `docker build` |
| Frozen meal | **Image** (read-only, identical every time) |
| Heating it up and serving | `docker run` |
| Plate of food | **Container** (a running copy) |
You can serve many plates from one frozen meal. That's what replicas are.

**"What are the YAML files?"**
Descriptions of **what I want to exist**. Kubernetes makes it happen (that's called **declarative**).
- `namespace.yaml`: a folder called `nhd-demo` for my stuff.
- `deployment.yaml`: "always keep 3 copies of my app running."
- `service.yaml`: "give those copies one shared address and spread requests between them."
They connect through a **label** (`app: nhd-demo`), like a name tag: the Service sends traffic to any pod wearing that tag.

**"Where did the Prometheus metrics page come from? I thought I had to set that up."**
Two different things:
- **`/metrics`** = my app's page of current numbers. We built it in Step 2. Like a **thermometer** in each room.
- **Prometheus server** = a separate program (Step 5) that visits every pod's `/metrics` every 15–30 seconds and **records the history**. Like a person writing down every thermometer reading in a notebook. **Grafana** (Step 6) draws charts from that notebook.

**"What does Ctrl+C do?"**
Stops the command running in that terminal. It sends a "please stop" signal (**SIGINT**). It doesn't delete anything in Kubernetes: the pods keep running.

### Where I got stuck, and how it was resolved

**"It failed" when testing the app.**
Claude ran the same tests and they passed, so the problem was in how the commands were run. Common causes:
| Error | Cause | Fix |
|---|---|---|
| `go.mod file not found` | In the wrong folder | `cd app` first |
| `zsh: no matches found` | Pasted the `# comments` too | Paste only the commands |
| `Failed to connect` | Server not running | Keep `go run .` open in terminal 1 |
| `address already in use` | An old server is still running | Ctrl+C in that terminal |
| `command not found: go` | Terminal opened before Go was installed | Open a new terminal |

**Where to start testing again.** The image was already built and pushed, but an old `go run .` was still using port 8080. Fix: Ctrl+C that terminal first (or `kill <process number>`), then `docker run`.

**The pods wouldn't start: `CreateContainerConfigError`.**
- *Simply:* I told Kubernetes "don't run this as the admin user (root)." The image said "run as the user named *nonroot*." Kubernetes only checks users by **number** (root is always 0), so it couldn't tell whether "nonroot" was secretly 0, and refused. Like a bouncer who can only check a date of birth, being shown a name tag that says "Adult."
- *Fix:* tell Kubernetes the number: `runAsUser: 65532`.
- *How we found it:* `kubectl describe pod <name>`, then scroll to **Events** at the bottom and look for the `Warning` line. It said: `container has runAsNonRoot and image has non-numeric user (nonroot), cannot verify user is non-root`.
- *Lesson:* pod won't start → `kubectl describe pod` → Events. Pod started then crashed → `kubectl logs`.

**Couldn't find the Rancher Desktop window.**
The menu-bar icon was probably hidden behind the MacBook's **notch** (too many menu-bar icons). Fix: open it from Spotlight (Cmd+Space, "Rancher"), or run `open -a "Rancher Desktop"`.

**Port Forwarding showed `http`, not port 80.**
`http` is the **name** we gave the port in the YAML. Same port. Rancher Desktop shows the name instead of the number.

**`kubectl get pods -w` was hard to read.**
`-w` (watch) only prints a new line each time a pod **changes**, so you can't see the total. Better options:
- `kubectl -n nhd-demo get deployment nhd-demo -w` shows `READY 3/3 → 2/3 → 3/3`.
- `while true; do clear; kubectl -n nhd-demo get pods; sleep 1; done` refreshes the whole list every second.

**Deleted pods showed `Error`.**
Kubernetes stops a pod by sending **SIGTERM** ("please shut down"). The app didn't handle it, so it was killed mid-work. Fixed with **graceful shutdown** (version 0.2.0): catch the signal, finish current requests, exit cleanly. Now deleted pods show **`Completed`**. Also added a 5-second **preStop** pause so the Service stops sending traffic to the pod before it shuts down.

### Things Claude got wrong (and corrected)
- **"`docker ps` will be empty after stopping the container."** Not true here. It also lists Kubernetes' own containers (names starting `k8s_`), because Rancher Desktop's Docker engine is shared with Kubernetes. They're normal: leave them alone.
- **"Rancher Desktop's port forward breaks if you delete its pod."** Wrong for Rancher Desktop: it **reconnected to another pod automatically**, which is why my browser showed a new name. Plain `kubectl port-forward` **does** break (`error: lost connection to pod`). Claude tested this to confirm.

### Things that looked like errors but weren't
- **Homebrew "tap trust" warning** while installing Go: about a different, third-party Homebrew source already on my Mac (`sidneys/homebrew`). Go came from the official source, so it's unrelated.
- **First curl printed nothing:** curl ran before the app had started. Retry with `--retry`. (This is why Kubernetes has readiness probes.)
- **`docker exec ... sh` fails with `"sh": executable file not found`:** on purpose. Distroless images have no shell, so an attacker has nothing to work with.
- **`couldn't attach to pod ... falling back to streaming logs`:** the test pod finished too fast for kubectl to attach. Output still came through.
- **Load balancing isn't in strict order:** Kubernetes picks a pod at random for each new connection. Over many requests it evens out.
- **Port-forward always shows the same pod name:** port-forward tunnels to **one** pod. It's a testing tool, not load balancing.

### Ideas to be able to explain in an interview
- **Image vs container:** image = read-only package; container = running copy. One image, many containers.
- **Multi-stage build:** compile in a big image, ship only the result in a tiny one. Smaller, faster, fewer security holes.
- **Why not run as root:** if the app is hacked, the attacker doesn't get admin rights.
- **Desired state / reconciliation:** I say "3 replicas"; Kubernetes keeps comparing that to what's running and fixes any difference. That's **self-healing**.
- **Readiness vs liveness probe:** readiness failing = stop sending this pod traffic (no restart). Liveness failing = restart the container.
- **Service:** one stable name and address in front of pods that come and go. Finds them by **label**.
- **Rolling update:** new image tag → new pods start, become ready, then old ones are removed. No downtime. `kubectl rollout undo` goes back.
- **Graceful shutdown:** SIGTERM → finish current requests → exit. After 30s, Kubernetes sends SIGKILL, which can't be caught.
- **Pull model:** Prometheus fetches `/metrics` from the app; the app doesn't send its data.
- **Label cardinality:** never put unlimited values (like any URL a user types) in a metric label, or Prometheus fills up its memory.

### Debugging checklist
```sh
kubectl -n nhd-demo get pods                  # what's the STATUS?
kubectl -n nhd-demo describe pod <name>       # won't start? read Events, look for Warning
kubectl -n nhd-demo logs <name>               # started but crashed? read the app's output
kubectl -n nhd-demo get endpointslices        # which pods is the Service sending traffic to?
lsof -nP -iTCP:8080 -sTCP:LISTEN              # what's using port 8080 on my Mac?
```

### Quick self-test
1. What's the difference between an image and a container?
2. Why is the final image 18MB when the Go image is 1.32GB?
3. Why does `instrument` take `path` as a fixed string instead of reading the URL? What could go wrong otherwise?
4. A pod is running but "not ready" and gets no traffic. Which endpoint and which probe are involved? How is that different from a liveness failure?
5. Your manager asks for p95 latency and error rate. Which metric answers each, and why is one a counter and the other a histogram?
6. Pods are stuck in `CreateContainerConfigError`. What command do you run, and where do you look?
7. Why did `runAsNonRoot` need a number instead of the name `nonroot`?
8. You delete a pod. Why does a new one appear, and why does it have a different name?
9. What does the 5-second `preStop` pause protect against?
10. What's the difference between the `/metrics` page and the Prometheus server?

---

## Step 5 — Install Prometheus + Grafana (2026-11-01)

### What we did
```sh
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update prometheus-community
helm install monitoring prometheus-community/kube-prometheus-stack \
  --version 92.17.0 --namespace monitoring --create-namespace -f monitoring/values.yaml
kubectl -n monitoring get pods                 # 6 pods Running, 0 restarts
```
The Grafana password is generated at install and kept in a Secret, **not in this repo**:
```sh
kubectl -n monitoring get secret monitoring-grafana -o jsonpath='{.data.admin-password}' | base64 -d; echo
kubectl -n monitoring port-forward svc/monitoring-grafana 3000:80    # http://localhost:3000, user admin
kubectl -n monitoring port-forward svc/monitoring-kube-prometheus-prometheus 9090:9090    # http://localhost:9090/targets
```

### What got installed
| Pod | Job |
|---|---|
| `prometheus-...-prometheus-0` | Scrapes `/metrics` pages and stores the history |
| `monitoring-grafana-...` | Draws charts; already connected to Prometheus |
| `alertmanager-...` | Sends alerts when rules trigger |
| `monitoring-kube-prometheus-operator-...` | Turns ServiceMonitors into Prometheus config |
| `monitoring-kube-state-metrics-...` | Reports Kubernetes objects' state, e.g. ready replicas |
| `monitoring-prometheus-node-exporter-...` | Reports the machine: disk, memory, network |

### Concepts to know
- **Helm chart**: a package of Kubernetes manifests. **Pin the version** for the same reason you pin image tags.
- **`monitoring/values.yaml`**: overrides for the chart. It turns off scraping of `kubeControllerManager`, `kubeEtcd`, `kubeScheduler` and `kubeProxy`, because k3s runs them in one process and doesn't expose them. Left on, they show as permanently failing targets. It's outside `k8s/` because it's Helm input, not a manifest for `kubectl apply`.
- **Exporter**: a helper that publishes metrics about something that doesn't publish its own, e.g. node-exporter.
- **`Watchdog` alert**: always fires, on purpose, to prove the alerting pipeline works.

### What broke / observations
- 13 targets up, but `monitoring-grafana` first showed `unknown`. It just hadn't been scraped yet.
- Node memory went from 27% to 60% (about 3.5 GB of 5.8 GB). First thing to check if the cluster gets sluggish.
- A `curl` alert check failed with a Python `KeyError`: `curl` treats `{}` in a URL as a pattern. **Fix:** `curl -G --data-urlencode 'query=...'`.

---

## Step 6 — Scrape the app + Grafana dashboard (2026-11-01)

### Scraping: `monitoring/servicemonitor.yaml`
A **ServiceMonitor** tells Prometheus which Service to scrape.
```sh
kubectl apply -f monitoring/servicemonitor.yaml
```
| Field | Why |
|---|---|
| `labels: release: monitoring` | Prometheus only picks up ServiceMonitors with this label. Without it, the file is **ignored with no error**. |
| `selector: app: nhd-demo` | Finds the app's Service |
| `port: http`, `path: /metrics`, `interval: 15s` | What to scrape and how often |

Targets appeared after about 30 seconds: `serviceMonitor/nhd-demo/nhd-demo/0`, `3 / 3 up`. Prometheus uses the Service to find the pods, then scrapes each **pod IP directly on 8080**. Going through the Service would hit a random pod each time, and each pod keeps its own counts.

The chart's own components are scraped the same way: it created 9 ServiceMonitors at install (`kubectl get servicemonitors -A`).

### Dashboard: `monitoring/dashboard.yaml`
A ConfigMap labelled `grafana_dashboard: "1"`. A sidecar container in the Grafana pod loads it, so the dashboard is tracked in Git and recreated automatically.

| Panel | Query |
|---|---|
| Request rate | `sum by (code) (rate(http_requests_total{namespace="nhd-demo", path="/"}[1m]))` |
| Health check rate | Same, for `path="/healthz"`. About 0.9/s: 0.3/s per pod × 3 pods. |
| Duration: average | `rate(..._sum[1m])` ÷ `rate(..._count[1m])` |
| Duration: p95 | `histogram_quantile(0.95, sum by (le) (rate(..._bucket[1m])))` |
| Pod CPU | `sum by (pod) (rate(container_cpu_usage_seconds_total{namespace="nhd-demo", pod=~"nhd-demo-.*", container=""}[2m])) * 1000` |
| Pod memory | `sum by (pod) (container_memory_working_set_bytes{..., container=""})`, plus the limit from `kube_pod_container_resource_limits` |

### Image 0.3.0: smaller histogram buckets
`/metrics`, the counter and the histogram already existed from Step 2, so at first no rebuild was needed. Building the dashboard then showed a real reason for one: `prometheus.DefBuckets` starts at 5ms, but requests take about 0.02ms.

```sh
docker build -t nhd-demo:0.3.0 app/
kubectl apply -f k8s/
kubectl -n nhd-demo rollout status deployment/nhd-demo
```
| Metric | Before | After |
|---|---|---|
| p50 | not checked | 0.014 ms |
| p95 | 4.75 ms (meaningless) | 0.05 ms |
| Average | 0.015 ms | 0.022 ms |

### Concepts to know
- **Why p95 was stuck at 4.75 ms:** `histogram_quantile` only knows which bucket a request fell in, then estimates inside it. With everything in the 0–5ms bucket, the 95% point is 95% of the way through: 4.75ms, whatever the real speed.
- **Choose buckets around your real latency.** Every bucket is another time series per path and per pod, so don't add them without limit.
- **Test traffic in the same namespace:** the `load-test` pod shows up in pod queries too, so the CPU and memory panels filter with `pod=~"nhd-demo-.*"`.

### What broke / observations
- **CPU and memory queries returned nothing** with `container="app"`. On this setup, the kubelet's cAdvisor only reports pod-level numbers, with an empty `container` label. **Fix:** filter on `container=""`. Built-in dashboards that expect per-container numbers may have empty panels here.
- **Counters reset on rollout:** new pods start at zero. `rate()` handles this, which is one reason to use it over raw counts.
- The running `0.3.0` image was built just before a `gofmt` whitespace fix to `main.go`, so behaviour is identical.
- No failed-request count was measured during this rollout, unlike `0.2.0`.
- The dashboard's data was checked by running every query through Grafana. Claude never saw the charts rendered. A colourblind-safety check on Grafana's default colours printed no result, so they're unverified.
- `prom/prometheus:v3.5.0` was pulled locally only to run `promtool`. `docker rmi prom/prometheus:v3.5.0` removes it.

---

## Study notes: Session 3, Prometheus and Grafana (2026-11-01)

A plain-English recap of the third session, for review before the interview.

### What we built
1. **Confirmed `/metrics` was already working** and in the right format, using Prometheus's own checker, `promtool`.
2. **Installed the monitoring stack** with Helm into a `monitoring` namespace.
3. **Told Prometheus about my app** with a ServiceMonitor. All 3 pods showed as up.
4. **Built a Grafana dashboard** with request rate, health check rate, average and p95 duration, and pod CPU and memory. It's a file in the repo.
5. **Released version 0.3.0** with smaller timing buckets, so p95 shows my app's real speed.

### Questions I asked, and the answers

**"When did we set up Prometheus already?"**
We hadn't. In Step 2 we added the Prometheus **client library**, which is code inside my app that keeps counts and publishes `/metrics`. The Prometheus **server** is a separate program that visits that page and saves the history. Same name, different things.

**"What am I looking at on the 9090 targets page?"**
Prometheus's checklist of every metrics page it visits, and how the latest visit went. The 3 `nhd-demo` endpoints are my 3 pods.

**"What are all these other metrics pages that aren't my app?"**
The same kind of page, published by other programs: Kubernetes, the machine, and the monitoring tools themselves. Pod CPU and memory come from one of them, the kubelet, so I don't need code for those.

**"Why is Prometheus already watching those, but needs a ServiceMonitor for my app?"**
They have ServiceMonitors too. The chart created 9 of them at install. It just doesn't know my app exists, so I write that one myself.

**"What does 'every query returns real data' mean?"**
Each chart is driven by a question to Prometheus. Claude asked each one and got real numbers back. That proves the numbers work, not that the page looks right.

**"Why should I do 0.3.0?" and "What does p95 mean?"**
p95 is the time 95% of requests were faster than. Line up 100 requests from fastest to slowest, and it's the 95th. It was stuck because my stopwatch only had one mark, at 5ms, and every request finished before it. Smaller buckets are more marks on the stopwatch.

### Things Claude got wrong (and corrected)
- **"`/healthz` should show about 0.6 requests per second."** The right number is about 0.9: 0.3 per pod × 3 pods. It measured 0.93.
- **First CPU and memory queries returned nothing.** Claude assumed a `container="app"` label that this setup doesn't have.
- **A failed alert check** was a bug in Claude's `curl` command, not a cluster problem.

### Things that looked like errors but weren't
- **`Watchdog` alert firing:** it fires on purpose.
- **A target showing `unknown`:** it hadn't been scraped yet.
- **No `code="404"` line on `/metrics`:** a label combination only appears after it first happens.
- **Charts dropping to zero when test traffic ends:** there's no traffic to measure. The average and p95 panels go empty, because you can't divide by zero requests.
- **New pod names on the charts after a rollout:** the rollout replaced the pods.

### Ideas to be able to explain in an interview
- **Client library vs server:** the app publishes numbers; Prometheus collects and stores them (the **pull model**); Grafana draws them.
- **ServiceMonitor:** tells Prometheus what to scrape. The most common reason a target never appears is a missing `release` label.
- **Why scrape pods, not the Service:** each pod keeps its own counts, and the Service would hit a random one.
- **Counter vs histogram:** counter = how many (use `rate()`); histogram = how long, in buckets (use `histogram_quantile()`).
- **Why p95 over average:** with 95 requests at 1ms and 5 at 500ms, the average is about 26ms, which nobody experienced. Averages hide slow requests.
- **Percentiles are only as good as the buckets:** my p95 read 4.75ms when real requests took about 0.02ms.
- **Dashboards as code:** kept in Git, so they're recreated automatically and reviewable.
- **Secrets stay out of Git:** the Grafana password lives in a Kubernetes Secret.

### Debugging checklist
```sh
kubectl get servicemonitors -A --show-labels    # target missing? does it have release=monitoring?
kubectl -n nhd-demo get svc --show-labels       # does the Service match the selector?
kubectl -n monitoring get pods                  # is the stack healthy?
```
Then check http://localhost:9090/targets: missing means Prometheus was never told about it; DOWN means it tried and failed, and the row shows the error.

### Quick self-test
1. What's the difference between the Prometheus client library and the Prometheus server?
2. Your ServiceMonitor applied without errors, but no target appears. What do you check first?
3. Why does Prometheus scrape each pod directly instead of going through the Service?
4. Why did p95 read 4.75ms when requests took about 0.02ms?
5. Why is `/healthz` about 0.9 requests per second?
6. Where do pod CPU and memory numbers come from, given the app doesn't report them?
7. Why use `rate()` instead of a counter's raw value?
8. Why can an average look fine while some users have a bad experience?
9. How does the dashboard get into Grafana without clicking anything?
10. Why isn't the Grafana password in this repo, and how do you get it?

---
