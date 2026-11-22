# Security

## Reporting a vulnerability

Please **don't open a public issue** for security problems. Report them privately in either of these ways:
- **On GitHub (preferred):** on this repository's **Security** tab, choose **Report a vulnerability** ([GitHub's guide](https://docs.github.com/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)).
- **By email:** write to [support@mubarak-j.com](mailto:support@mubarak-j.com).

Please include what you found, how to reproduce it, and what an attacker could do with it. Reports are handled on a best-effort basis. Expect an acknowledgement within a week, and a fix or mitigation as soon as practical. You're welcome to be credited in the advisory.

In scope:
- this repository's code: the CLI, the GitHub Action and the App server;
- the hosted tf-plan-cost GitHub App at `tf-plan-cost.mubarak-j.com`.

Please don't run load tests or denial-of-service tests against the hosted App.

Supported versions: the latest `v1.x` release. Fixes aren't backported.

## What the hosted GitHub App receives and keeps

**What it receives** (app mode only):
- The output of `tf-plan-cost strip`. That's your plan's resource changes, with every value Terraform marks as sensitive replaced by `null`, plus each AWS provider's `region` expression and the root variables those expressions use. Prior state, outputs, other provider arguments and unrelated variables are removed in your CI before upload. Run `tf-plan-cost strip plan.json` to see exactly what is sent.
- Your workflow's GitHub Actions OIDC token, which proves which repository and run the plan comes from.
- Optional query parameters: the comment ID, config path and default region.

**What it does with it:**
- Prices the plan **in memory** and discards it when the request ends. Plans and tokens are never written to disk, stored or logged.
- Sends AWS only price queries: service, region and product attributes such as `instanceType=m6i.large`. It never sends plan contents.
- Logs one line per request to CloudWatch, kept for 14 days. The line holds the repository, PR number, workflow run ID, head commit and the monthly cost change, plus error messages.

**What it can access on GitHub:** only repositories where you install the App, with the permissions shown during installation:
- Checks (read and write), to post the Check Run;
- Pull requests (read and write), to post the comment;
- Contents (read), to read `.tf-plan-cost.yml`;
- Metadata (read).

It runs on AWS Lambda behind CloudFront. Its private key and webhook secret are stored in AWS SSM Parameter Store.

If you'd rather not send plans anywhere, use **action mode**. Pricing then happens inside your own workflow, and only price queries leave it. You can also run your own App server ([`deploy/aws`](deploy/aws/README.md)).

## How requests are authenticated

- **Plan uploads:** the GitHub Actions OIDC token is verified against GitHub's published keys, along with the issuer (`https://token.actions.githubusercontent.com`), the audience (`tf-plan-cost`) and the expiry.
  - The repository, PR and commit are taken **only from the token's claims**, never from the request.
  - The App must be installed on that repository. The installation token it uses is limited to that one repository, and the repository's ID is checked against the token.
- **Webhooks:** the `X-Hub-Signature-256` HMAC is checked in constant time before the payload is parsed.
- **Pull requests from forks** get no OIDC token from GitHub, so they can't upload plans.
- **Outdated runs** can't overwrite a newer result: the comment is only updated when the plan's commit is still the PR's head.

## Releases

Each release includes a `checksums.txt`. The GitHub Action verifies the downloaded binary against it, and stops if they don't match.

Releases built after the repository became public also have build provenance attestations:

```sh
gh attestation verify tf-plan-cost_<version>_linux_amd64.tar.gz --repo mubarak-j/tf-plan-cost
```
