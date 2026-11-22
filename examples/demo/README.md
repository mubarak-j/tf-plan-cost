# tf-plan-cost demo

A small AWS web stack you can use to watch tf-plan-cost work on real pull requests. **You don't need an AWS account**:
- the AWS provider uses mock credentials;
- `demo.tfstate` is a made-up state matching `main.tf`, so `terraform plan -refresh=false` never calls AWS.

Nothing here can be applied, and nothing is ever created.

| Resource | Monthly cost |
|---|---|
| 2 × `aws_instance` (m6i.large, 30 GB gp3 root) | $144.96 |
| `aws_db_instance` (db.m6g.large PostgreSQL, gp3 100 GB) | $127.57 |
| `aws_nat_gateway` | $32.85 |
| `aws_lb` (application) | $16.43 |
| `aws_ebs_volume` (gp3 200 GB) | $16.00 |
| `aws_s3_bucket` | not priced (shown as unsupported) |
| **Total** | **$337.81** |

These are us-east-1 on-demand prices as of November 2026. The comment always uses current prices.

## Try it

1. **Create a repository** with the contents of this folder at its root:
   ```sh
   mkdir tf-plan-cost-demo && cd tf-plan-cost-demo && git init
   cp -r path/to/tf-plan-cost/examples/demo/. .
   mkdir -p .github/workflows && mv tf-plan-cost.yml .github/workflows/
   git add -A && git commit -m "tf-plan-cost demo" && git push   # to a new GitHub repository
   ```
2. **Install the [tf-plan-cost App](https://github.com/apps/tf-plan-cost)** on that repository.
3. **Open a pull request** that changes a size in `main.tf`, for example:
   ```hcl
   variable "web_instance_type" {
     default = "m6i.xlarge" # was m6i.large
   }
   ```
   The workflow plans the change, and tf-plan-cost adds a **Check Run** and a **comment**: `+$140.16/mo (was $337.81 → now $477.97)`.

Other changes to try: `web_count = 4`, `db_storage_gb = 500`, `db_instance_class = "db.r6g.xlarge"`, or delete the NAT gateway (the cost goes down). To see a failing check, add a `.tf-plan-cost.yml` with `fail_above_monthly_usd: 100`.

To try it without the App, use action mode instead. Replace `mode: app` with your own AWS credentials, allowed to call `pricing:GetProducts`, and `pull-requests: write` (see the main [README](../../README.md#github-action-mode)).

## Keeping the state in sync

If you change `main.tf` on the default branch, regenerate `demo.tfstate` so the unchanged config plans "No changes". The commands are at the top of [`tools/make-state.py`](tools/make-state.py). This repository's CI checks that `main.tf` and `demo.tfstate` still match.
