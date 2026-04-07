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
