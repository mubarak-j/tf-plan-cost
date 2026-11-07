variable "github_repositories" {
  description = <<-EOT
    Repositories whose workflows may assume the role. Prefer the ID form
    owner@OWNER_ID/repo@REPO_ID, which keeps trusting the same repository if
    its name is ever reused. Plain owner/repo also works.
  EOT
  type        = list(string)

  validation {
    condition = length(var.github_repositories) > 0 && alltrue([
      for r in var.github_repositories :
      can(regex("^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$", r)) || can(regex("^[A-Za-z0-9-]+@[0-9]+/[A-Za-z0-9._-]+@[0-9]+$", r))
    ])
    error_message = "Use owner/repo or owner@OWNER_ID/repo@REPO_ID, e.g. mubarak-j@83465122/tf-plan-cost@1449032169. Wildcards are not allowed."
  }
}

variable "role_name" {
  description = "Name of the IAM role."
  type        = string
  default     = "tf-plan-cost-pricing"
}

variable "create_oidc_provider" {
  description = "Create the GitHub OIDC provider. Set to false if the account already has one."
  type        = bool
  default     = true
}

variable "region" {
  description = "Region for the AWS provider. IAM is global, so this rarely matters."
  type        = string
  default     = "us-east-1"
}
