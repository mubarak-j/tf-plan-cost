locals {
  function_url = trimsuffix(aws_lambda_function_url.this.function_url, "/")
  public_url   = local.custom_domain ? "https://${var.domain_name}" : local.function_url
}

output "url" {
  description = "Server URL: use it as the Action's app-url."
  value       = local.public_url
}

output "webhook_url" {
  description = "Webhook URL for the GitHub App settings."
  value       = "${local.public_url}/webhook"
}

output "function_url" {
  description = "The Lambda Function URL (works with or without a custom domain)."
  value       = local.function_url
}

output "certificate_validation_record" {
  description = "DNS record that proves domain ownership to ACM (custom domain, step 1)."
  value = local.custom_domain ? [for o in aws_acm_certificate.this[0].domain_validation_options : {
    type  = o.resource_record_type
    name  = o.resource_record_name
    value = o.resource_record_value
  }] : []
}

output "domain_record" {
  description = "DNS record pointing the custom domain at CloudFront (custom domain, step 2)."
  value = local.custom_domain ? {
    type  = "CNAME"
    name  = var.domain_name
    value = aws_cloudfront_distribution.this[0].domain_name
  } : null
}
