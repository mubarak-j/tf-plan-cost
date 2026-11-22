variable "region" {
  description = "AWS region for the function. Prices for every region are read from the us-east-1 Price List API either way."
  type        = string
  default     = "us-east-1"
}

variable "name" {
  description = "Name of the Lambda function, IAM role and log group."
  type        = string
  default     = "tf-plan-cost"
}

variable "package" {
  description = "Path to the zip built by build.sh."
  type        = string
  default     = "build/tf-plan-cost.zip"
}

variable "ssm_prefix" {
  description = "SSM Parameter Store path holding app-id, private-key and webhook-secret (put there with the AWS CLI, see README)."
  type        = string
  default     = "/tf-plan-cost"
}

variable "web_adapter_layer_version" {
  description = "Version of the AWS Lambda Web Adapter layer (LambdaAdapterLayerArm64)."
  type        = number
  default     = 30
}

variable "reserved_concurrency" {
  description = "Maximum concurrent executions, which caps cost if the public URL is flooded. null = no reservation (needed when the account's concurrency limit is too low to reserve any)."
  type        = number
  default     = 5
}

variable "log_retention_days" {
  description = "CloudWatch Logs retention."
  type        = number
  default     = 14
}
