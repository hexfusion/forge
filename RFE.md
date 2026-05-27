# Forge Pipeline RFEs

From first end-to-end RHOAI CI image testing run (2026-04-06).

## 1. Pre-deploy env var validation

`forge pipeline deploy` should check that the target deployment has the expected
`RELATED_IMAGE_*` env vars before patching. If a var doesn't exist on the CSV,
warn the user and offer to add it.

**Context:** RHOAI 3.4.0-ea.1 didn't have `RELATED_IMAGE_ODH_LLM_D_KV_CACHE_IMAGE`.
The deploy succeeded but the image was never injected.

## 2. Operator version detection

Before deploying, detect the installed CSV version and warn if the pipeline's
images are from a newer version than the operator. Prevents silent failures
where the operator code doesn't know how to use the injected image.

## 3. CRD dependency check

Pipeline def declares required CRDs:

```yaml
requires:
  crds:
    - authpolicies.kuadrant.io
    - inferencepools.inference.networking.k8s.io
```

`forge pipeline deploy` validates they exist before creating CRs that depend
on them. Generic — not tied to any specific operator.

## 4. Namespace gateway pre-check

Validate the target namespace is allowed by the referenced gateway's listener
config before deploying. Prevents HTTPRoute `NotAllowedByListeners` errors.

## 5. `forge pipeline cleanup`

Restore operator to original state — revert CSV patches, delete test
LLMInferenceService CRs, scale controllers back up. Undo what deploy did.

## 6. Git-branch builds for upstream-fix testing ✅ Implemented

From the vllm#37581 audio-render bug fix workflow (2026-05-20).

Today `source: build` builds from whatever's checked out in `Local`. To test
an upstream fix on a branch, you have to manually checkout, build, push, then
edit the pipeline to reference the new tag.

Add git-branch handling so a pipeline def can declare which branch to build
and forge handles the checkout + tag template:

```yaml
images:
  vllm:
    source: build
    local: ~/projects/vllm-project/vllm
    build_file: Dockerfile
    git:
      branch: fix-37581           # required; defaults to current HEAD if omitted
      remote: origin              # optional; if set, forge fetches first
      worktree: /tmp/forge-vllm   # optional; uses git worktree to avoid
                                  # touching the user's working checkout
    registry: quay.io:443/sbatsche
    name_override: vllm
    tag_template: "{branch}-{short_sha}"  # vllm:fix-37581-a3b7c2
```

Requirements:
- `git:` is a new sub-struct on `PipelineImage`.
- `forge pipeline ship` resolves the branch (and SHA) before invoking the
  builder; doesn't modify the user's working directory if `worktree:` is set.
- `tag_template:` supports `{branch}`, `{short_sha}`, `{full_sha}`,
  `{timestamp}` placeholders.
- Generic for any Containerfile-based build, not just Go-binary builds.
  Existing `BuildTarget`/`BinaryName` stay opt-in for the Go path.

**Context:** vLLM CUDA builds are ~30-60 min + ~10 GB. Patch-test cycles for
upstream fixes (e.g., vllm#37581) need a deterministic build-tag-push loop
without disturbing the dev checkout. Same pattern applies to kserve,
llm-d-router, and any other repo where we want to test branches against the
cluster.

## 7. Build-host abstraction

Related to #6 but separable. `source: build` today builds wherever forge
runs. For heavy CUDA builds that's the dev laptop — often the wrong place.

Add a `build_host:` field to delegate the build:

```yaml
images:
  vllm:
    source: build
    build_host:
      type: ssh                   # ssh | tekton | buildah-bud-remote
      host: builder.dagobah       # for ssh
      # or: cluster-buildconfig for in-OCP build
```

Defaults to local. SSH-remote is the smallest viable second backend (rsync
the source, run podman/docker over ssh, push from the remote directly).

## 8. Declarative CR/manifest apply (deploy shapes)

From the audio/video PR #1115 validation (2026-05-27).

`deploy` today only patches operator *images* (`method: env-patch` on the
CSV). It can't apply the *CRs* that define the deployment **shape** — native
`InferencePool` + agentgateway (upstream) vs `LLMInferenceServiceConfig`
(downstream). As a result `audio-video-pr1115-upstream.yaml` and
`-downstream.yaml` are identical except for a `--shape` flag passed to
external validate scripts; the shape lives in shell, not in forge.

Add a manifest-apply step so the shape lives in the pipeline def:

```yaml
deploy:
  kube_context: ...
  namespace: ...
  target_deployment: rhods-operator
  method: env-patch
  manifests:                      # oc apply -f, after the image patch + rollout
    - ~/.../crs/upstream/inferencepool.yaml
    - ~/.../crs/upstream/gateway.yaml
  # or a directory applied in sorted order:
  # manifest_dir: ~/.../crs/upstream
```

Requirements:
- `manifests:` / `manifest_dir:` on `PipelineDeploy`; applied after the
  env-patch + rollout, in a deterministic order.
- `forge pipeline cleanup` (#5) deletes them on teardown.
- Generic `oc apply` — not tied to llm-d CR kinds.

**Result:** the two PR #1115 defs would differ only in `manifests:`, and the
`--shape` flag disappears from the validate scripts.

## 9. Side-by-side instances (deployment-scoped deploy)

Also from the PR #1115 work.

`env-patch` mutates the operator CSV — a cluster singleton. So two instances
(`audio-video-baseline` and `audio-video-pr1115`) can't run at once: shipping
the candidate swaps the baseline's images on the one shared stack. A/B is
therefore sequential, and you lose the baseline the moment you ship the
candidate.

Add a deploy method that stands the candidate up in its **own namespace**
instead of the shared CSV, so baseline and candidate coexist:

```yaml
deploy:
  method: standalone              # vs env-patch (CSV, singleton)
  kube_context: ...
  namespace: llm-d-pr1115         # instance-scoped; created if absent
  manifests: ~/.../stacks/upstream   # full stack (Deployment/Service/
                                      # InferencePool/Gateway), image-substituted
```

Requirements:
- forge substitutes the freshly built image ref into the stack's workload
  before apply (placeholder swap or kustomize image override).
- Instance-scoped namespace ⇒ operator-managed baseline and standalone
  candidate run in parallel; A/B is concurrent, not a destructive swap.
- This is also the "upstream really means a non-RHOAI llm-d-native install"
  path noted in #6's caveats.
- Pairs with #8 (each namespace gets its own shape CRs) and #5 (cleanup
  deletes the namespace/stack).

**Note:** a deployment-scoped *patch* against the OLM-managed operator is the
naive version, but OLM reconciles the deployment back to the CSV — so the
viable mechanism is a standalone stack the operator doesn't own, not a patch
it will revert.
