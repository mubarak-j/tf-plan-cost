output "role_arn" {
  description = "Use as role-to-assume in aws-actions/configure-aws-credentials."
  value       = aws_iam_role.pricing.arn
}
