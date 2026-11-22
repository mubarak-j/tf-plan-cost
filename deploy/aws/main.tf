# The tf-plan-cost GitHub App server on AWS Lambda.
#
# `tf-plan-cost serve` runs unchanged behind the AWS Lambda Web Adapter,
# which turns Function URL invocations into HTTP requests to localhost.
# Secrets stay in SSM Parameter Store and are never in this state file.

data "aws_caller_identity" "current" {}
data "aws_partition" "current" {}

locals {
  ssm_arn_prefix    = "arn:${data.aws_partition.current.partition}:ssm:${var.region}:${data.aws_caller_identity.current.account_id}:parameter${var.ssm_prefix}"
  web_adapter_layer = "arn:${data.aws_partition.current.partition}:lambda:${var.region}:753240598075:layer:LambdaAdapterLayerArm64:${var.web_adapter_layer_version}"
}

resource "aws_cloudwatch_log_group" "this" {
  name              = "/aws/lambda/${var.name}"
  retention_in_days = var.log_retention_days
}

resource "aws_iam_role" "this" {
  name = var.name
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" "this" {
  name = var.name
  role = aws_iam_role.this.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "Prices"
        Effect   = "Allow"
        Action   = "pricing:GetProducts"
        Resource = "*"
      },
      {
        # SecureStrings use the AWS managed key aws/ssm, whose key policy
        # allows decryption through SSM, so no kms:Decrypt grant is needed.
        Sid      = "Secrets"
        Effect   = "Allow"
        Action   = "ssm:GetParameter"
        Resource = "${local.ssm_arn_prefix}/*"
      },
      {
        Sid      = "Logs"
        Effect   = "Allow"
        Action   = ["logs:CreateLogStream", "logs:PutLogEvents"]
        Resource = "${aws_cloudwatch_log_group.this.arn}:*"
      },
    ]
  })
}

resource "aws_lambda_function" "this" {
  function_name    = var.name
  role             = aws_iam_role.this.arn
  runtime          = "provided.al2023"
  architectures    = ["arm64"]
  handler          = "bootstrap"
  filename         = var.package
  source_code_hash = filebase64sha256(var.package)
  memory_size      = 256
  timeout          = 120
  layers           = [local.web_adapter_layer]

  reserved_concurrent_executions = var.reserved_concurrency

  environment {
    variables = {
      PORT                         = "8080"
      TF_PLAN_COST_LISTEN_HOST     = "127.0.0.1"
      AWS_LWA_READINESS_CHECK_PATH = "/healthz"
      TF_PLAN_COST_CACHE_DIR       = "/tmp/tf-plan-cost-prices"
      GITHUB_APP_ID                = "ssm:${var.ssm_prefix}/app-id"
      GITHUB_PRIVATE_KEY           = "ssm:${var.ssm_prefix}/private-key"
      GITHUB_WEBHOOK_SECRET        = "ssm:${var.ssm_prefix}/webhook-secret"
    }
  }

  logging_config {
    log_format = "Text" # the server already writes JSON lines
    log_group  = aws_cloudwatch_log_group.this.name
  }

  depends_on = [aws_iam_role_policy.this]
}

# Public URL. The server authenticates every request itself: webhooks by
# their HMAC signature, plan uploads by the GitHub Actions OIDC token.
resource "aws_lambda_function_url" "this" {
  function_name      = aws_lambda_function.this.function_name
  authorization_type = "NONE"
}

# Function URLs created since October 2025 need both permissions.
resource "aws_lambda_permission" "url" {
  statement_id           = "FunctionURLAllowPublicAccess"
  action                 = "lambda:InvokeFunctionUrl"
  function_name          = aws_lambda_function.this.function_name
  principal              = "*"
  function_url_auth_type = "NONE"
}

resource "aws_lambda_permission" "invoke" {
  statement_id             = "FunctionURLAllowInvokeAction"
  action                   = "lambda:InvokeFunction"
  function_name            = aws_lambda_function.this.function_name
  principal                = "*"
  invoked_via_function_url = true
}
