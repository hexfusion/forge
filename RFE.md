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
