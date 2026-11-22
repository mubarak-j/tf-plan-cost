output "url" {
  description = "Server URL: use it as the Action's app-url."
  value       = trimsuffix(aws_lambda_function_url.this.function_url, "/")
}

output "webhook_url" {
  description = "Webhook URL for the GitHub App settings."
  value       = "${trimsuffix(aws_lambda_function_url.this.function_url, "/")}/webhook"
}

output "log_group" {
  value = aws_cloudwatch_log_group.this.name
}
