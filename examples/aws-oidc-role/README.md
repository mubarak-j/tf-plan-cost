# AWS role for tf-plan-cost

Creates an IAM role that GitHub Actions workflows in the repositories you list
can assume through OIDC (no long-lived keys), with one permission:
`pricing:GetProducts`. IAM roles and OIDC providers are free.

```sh
cd examples/aws-oidc-role
terraform init

# Recommended: pin the repository's immutable IDs.
REPO=$(gh api repos/your-org/your-repo --jq '"\(.owner.login)@\(.owner.id)/\(.name)@\(.id)"')
terraform apply -var "github_repositories=[\"$REPO\"]"

# Or by name only: terraform apply -var 'github_repositories=["your-org/your-repo"]'
```

GitHub's OIDC token subject may include immutable owner and repository IDs
(`repo:your-org@123/your-repo@456:pull_request`). With the ID form, the role
keeps trusting only the original repository, even if that name is later
deleted and recreated by someone else. A name-only entry matches both the
plain and the ID subject formats.

If your account already has the GitHub OIDC provider
(`token.actions.githubusercontent.com`), add `-var create_oidc_provider=false`.

Then use the `role_arn` output in your workflow:

```yaml
permissions:
  id-token: write
steps:
  - uses: aws-actions/configure-aws-credentials@v5
    with:
      role-to-assume: arn:aws:iam::123456789012:role/tf-plan-cost-pricing
      aws-region: us-east-1
```

The role ARN is not a secret; a repository variable is fine.
