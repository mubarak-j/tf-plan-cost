# An IAM role that GitHub Actions can assume with OIDC (no stored keys) and
# that can only read AWS prices. This is all tf-plan-cost needs.

terraform {
  required_version = ">= 1.6"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.81" # thumbprint_list is optional from 5.81
    }
  }
}

provider "aws" {
  region = var.region
}

locals {
  oidc_url = "https://token.actions.githubusercontent.com"

  # GitHub's OIDC subject comes in two forms:
  #   repo:mubarak-j/tf-plan-cost:pull_request                       (names)
  #   repo:mubarak-j@83465122/tf-plan-cost@1449032169:pull_request   (names + immutable IDs)
  # An entry with IDs trusts exactly that repository, even if the name is
  # later reused by someone else. A name-only entry matches either form.
  subject_patterns = flatten([
    for repo in var.github_repositories : strcontains(repo, "@") ? ["repo:${repo}:*"] : [
      "repo:${repo}:*",
      "repo:${split("/", repo)[0]}@*/${split("/", repo)[1]}@*:*",
    ]
  ])
  oidc_provider_arn = var.create_oidc_provider ? aws_iam_openid_connect_provider.github[0].arn : (
    data.aws_iam_openid_connect_provider.github[0].arn
  )
}

# An account can only have one provider per URL. If you already use GitHub
# OIDC, set create_oidc_provider = false to reuse it.
resource "aws_iam_openid_connect_provider" "github" {
  count          = var.create_oidc_provider ? 1 : 0
  url            = local.oidc_url
  client_id_list = ["sts.amazonaws.com"]
}

data "aws_iam_openid_connect_provider" "github" {
  count = var.create_oidc_provider ? 0 : 1
  url   = local.oidc_url
}

data "aws_iam_policy_document" "trust" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [local.oidc_provider_arn]
    }
    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }
    # Only workflows in these repositories (any branch or pull request).
    condition {
      test     = "StringLike"
      variable = "token.actions.githubusercontent.com:sub"
      values   = local.subject_patterns
    }
  }
}

resource "aws_iam_role" "pricing" {
  name                 = var.role_name
  description          = "tf-plan-cost: read AWS prices from GitHub Actions"
  assume_role_policy   = data.aws_iam_policy_document.trust.json
  max_session_duration = 3600
}

data "aws_iam_policy_document" "pricing" {
  statement {
    actions   = ["pricing:GetProducts"]
    resources = ["*"] # the Price List API has no resource-level permissions
  }
}

resource "aws_iam_role_policy" "pricing" {
  name   = "price-list-read"
  role   = aws_iam_role.pricing.id
  policy = data.aws_iam_policy_document.pricing.json
}
