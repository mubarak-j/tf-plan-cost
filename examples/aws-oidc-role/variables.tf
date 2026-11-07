variable "github_repositories" {
  description = "Repositories (owner/name) whose workflows may assume the role."
  type        = list(string)

  validation {
    condition     = length(var.github_repositories) > 0 && alltrue([for r in var.github_repositories : can(regex("^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$", r))])
    error_message = "Use owner/name, for example mubarak-j/tf-plan-cost. Wildcards are not allowed."
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
