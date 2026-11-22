# Candidate 823 data

This offline configuration targets only candidate resources in `coupling-dev-510710`, `europe-west1`. It does not create secret versions. The owner supplies values outside Terraform.

The GCS backend requires a separately reviewed candidate-only bucket; its prefix is `candidate-823/data`. Do not use the production state bucket or initialize a production working directory. The runtime root must use its own prefix and reference only this candidate data state.

Before any owner-authorized live apply, verify candidate-only backend access and quota headroom. Review a saved live plan against the exact candidate allowlist: create-only candidate resources, no imports or changes to existing resources, no secret values or versions, and only the two additive project IAM grants with the exact named-database condition. Check effective inherited IAM separately; conditional grants cannot remove existing broader access. Confirm that public can act as only the candidate invoker. A mock test is not a live plan or evidence of effective permissions.

Offline checks: `tofu init -backend=false`, `tofu fmt -check -recursive`, `tofu validate`, and `tofu test`.
