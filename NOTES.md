# DevOps Interview Prep — Lab Notes

Goal: local Kubernetes cluster → small Go app → deploy as a pod → Prometheus + Grafana monitoring.

## Plan

- [x] **Step 1** — Verify local Kubernetes cluster (Rancher Desktop)
- [ ] **Step 2** — Write a small Go web app with a `/metrics` endpoint
- [ ] **Step 3** — Containerize it (multi-stage Dockerfile)
- [ ] **Step 4** — Deploy to Kubernetes (Deployment + Service)
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
| Go | **Not installed** on the Mac |

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
