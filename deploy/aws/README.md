# Deploy the tf-plan-cost App on AWS Lambda

This deploys `tf-plan-cost serve` as a Lambda function with a public Function URL, then connects a GitHub App to it. The function runs the same binary as the CLI, with no Lambda-specific code, behind the [AWS Lambda Web Adapter](https://github.com/aws/aws-lambda-web-adapter).

```
GitHub ──webhook──▶ Function URL ─▶ Lambda Web Adapter ─▶ tf-plan-cost serve ─▶ AWS Price List API
Actions ─plan────▶ (auth: NONE; the server checks the        │
                    HMAC signature and the OIDC token)       └─▶ SSM Parameter Store (App secrets)
```

**What it creates:**
- a Lambda function: arm64, 256 MB, 120 s timeout, reserved concurrency 5;
- its Function URL;
- an IAM role allowed only `pricing:GetProducts`, `ssm:GetParameter` on `/tf-plan-cost/*`, and writing its logs;
- a CloudWatch log group with 14-day retention.

**Cost:** about $0 a month at low volume. Lambda's free tier covers 1M requests and 400,000 GB-seconds a month, and SSM standard parameters are free.

**Secrets** (the App ID, private key and webhook secret) go into SSM Parameter Store with the AWS CLI. The function reads them at start-up. They never appear in Terraform state, the function's configuration or this repository.

Requirements: Go 1.27+, Terraform 1.6+, the AWS CLI, `zip` and `openssl`.

The commands below use `--profile my-profile` and `us-east-1`. Use your own profile and the same region as the Terraform `region` variable.

## 1. Register the GitHub App

Open **[github.com/settings/apps/new](https://github.com/settings/apps/new)**. For an organization, use *Organization settings → Developer settings → GitHub Apps*.

First generate the webhook secret, and keep this terminal open:

```sh
WEBHOOK_SECRET=$(openssl rand -hex 32); echo "$WEBHOOK_SECRET"
```

| Field | Value |
|---|---|
| GitHub App name | `tf-plan-cost` (must be unique on GitHub) |
| Homepage URL | `https://github.com/mubarak-j/tf-plan-cost` |
| Callback URL, Setup URL | leave empty |
| Request user authorization (OAuth) during installation | unchecked |
| Enable Device Flow | unchecked |
| Webhook → **Active** | **unchecked for now**. You turn it on in step 4, once the URL exists. |
| Webhook secret | the `$WEBHOOK_SECRET` value |
| Repository permissions → **Checks** | Read and write |
| Repository permissions → **Contents** | Read-only (for `.tf-plan-cost.yml`) |
| Repository permissions → **Metadata** | Read-only (mandatory) |
| Repository permissions → **Pull requests** | Read and write (for the comment) |
| All other permissions | No access |
| Subscribe to events | hidden while the webhook is inactive; you subscribe to **Check run** in step 4 |
| Where can this GitHub App be installed? | **Only on this account** (switch to *Any account* when it goes public) |

Click **Create GitHub App**. Then:
1. Note the **App ID** at the top of the settings page.
2. Under **Private keys**, click **Generate a private key**. A `.pem` file downloads.

## 2. Store the secrets in SSM

```sh
export AWS_PROFILE=my-profile AWS_REGION=us-east-1

aws ssm put-parameter --name /tf-plan-cost/app-id --type String --value 'APP_ID'
aws ssm put-parameter --name /tf-plan-cost/private-key --type SecureString --value file://path/to/tf-plan-cost.*.private-key.pem
aws ssm put-parameter --name /tf-plan-cost/webhook-secret --type SecureString --value "$WEBHOOK_SECRET"
```

SecureStrings use the AWS managed key `aws/ssm`. Afterwards, move the `.pem` file to a password manager or delete it; GitHub can always generate a new key.

## 3. Build and deploy

```sh
cd deploy/aws
./build.sh                 # builds build/tf-plan-cost.zip for linux/arm64
terraform init
terraform apply
```

The outputs are `url` (the Action's `app-url`) and `webhook_url`. Check that the server is up:

```sh
curl "$(terraform output -raw url)/healthz"   # ok
```

If `terraform apply` fails with *"Specified ReservedConcurrentExecutions ... decreases account's UnreservedConcurrentExecution below its minimum value"*, your account's concurrency limit is too low to reserve any. New accounts can start at 10. Set `reserved_concurrency = null`, or ask AWS for a higher limit.

## 4. Turn on the webhook and install the App

1. In the App's settings, set **Webhook URL** to the `webhook_url` output, check **Active** and save.
2. Under **Permissions & events → Subscribe to events**, check **Check run** only and save. The section only appears while the webhook is active, and the event only once Checks is *Read and write*.
3. Under **Advanced → Recent deliveries**, redeliver any delivery, or check that new ones get `200` or `204`. A `401` means the webhook secret in GitHub doesn't match SSM.
4. Click **Install App**, choose your account, select **Only select repositories**, and pick the repositories to price.

## 5. Use it

```yaml
permissions:
  contents: read
  id-token: write

steps:
  - run: terraform show -json tfplan > plan.json
  - uses: mubarak-j/tf-plan-cost@main
    with:
      plan: plan.json
      mode: app
      app-url: https://<id>.lambda-url.us-east-1.on.aws # the `url` output
```

## Operating it

**Logs:**

```sh
aws logs tail /aws/lambda/tf-plan-cost --follow
```

The server logs one JSON line per request: repository, PR, run ID and cost delta. It never logs plans or tokens.

**Update the code:** run `./build.sh && terraform apply`. The URL stays the same.

**Rotate the private key or webhook secret:**
1. Create the new key or secret in the App settings. For the webhook secret, set it in GitHub after updating SSM.
2. `aws ssm put-parameter --overwrite --name /tf-plan-cost/... --value ...`
3. Restart the function so it reads the new values:
   ```sh
   aws lambda update-function-configuration --function-name tf-plan-cost --description "secrets rotated $(date -u +%FT%TZ)"
   ```
4. Delete the old private key in GitHub.

Don't run `terraform apply -replace` on the function. Replacing it changes the Function URL.

**Remove it:** run `terraform destroy`, then `aws ssm delete-parameters --names /tf-plan-cost/app-id /tf-plan-cost/private-key /tf-plan-cost/webhook-secret`, and delete the App in GitHub.

## Variables

| Variable | Default | Description |
|---|---|---|
| `region` | `us-east-1` | Region for the function and the SSM parameters |
| `name` | `tf-plan-cost` | Function, role and log group name |
| `ssm_prefix` | `/tf-plan-cost` | Parameter path |
| `reserved_concurrency` | `5` | Concurrency cap; `null` for none |
| `web_adapter_layer_version` | `30` | `LambdaAdapterLayerArm64` version |
| `log_retention_days` | `14` | Log retention |
| `package` | `build/tf-plan-cost.zip` | Zip from `build.sh` |

State is local (`terraform.tfstate`, gitignored). Keep it safe, or add a backend.
