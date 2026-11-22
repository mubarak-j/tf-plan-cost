# Optional custom domain: CloudFront in front of the Function URL, with an
# ACM certificate. DNS is managed outside AWS (e.g. Cloudflare), so the
# records are outputs to add by hand. See README "Custom domain".
#
# CloudFront forwards every viewer header except Host (Function URLs need
# their own Host), and caches nothing. The Function URL itself stays public:
# origin access control would require an x-amz-content-sha256 header on
# every POST, which GitHub webhooks do not send. That is fine because the
# server authenticates every request (webhook HMAC, OIDC token).

locals {
  custom_domain = var.domain_name != null
  origin_host   = trimsuffix(trimprefix(aws_lambda_function_url.this.function_url, "https://"), "/")
}

resource "aws_acm_certificate" "this" {
  count             = local.custom_domain ? 1 : 0
  provider          = aws.us_east_1
  domain_name       = var.domain_name
  validation_method = "DNS"

  lifecycle {
    create_before_destroy = true
  }
}

# Waits until the validation record exists in DNS (see README).
resource "aws_acm_certificate_validation" "this" {
  count           = local.custom_domain ? 1 : 0
  provider        = aws.us_east_1
  certificate_arn = aws_acm_certificate.this[0].arn
}

data "aws_cloudfront_cache_policy" "disabled" {
  name = "Managed-CachingDisabled"
}

data "aws_cloudfront_origin_request_policy" "all_except_host" {
  name = "Managed-AllViewerExceptHostHeader"
}

resource "aws_cloudfront_distribution" "this" {
  count           = local.custom_domain ? 1 : 0
  enabled         = true
  comment         = "${var.name} App server"
  aliases         = [var.domain_name]
  price_class     = "PriceClass_100"
  http_version    = "http2and3"
  is_ipv6_enabled = true

  origin {
    origin_id   = "lambda"
    domain_name = local.origin_host
    custom_origin_config {
      http_port                = 80
      https_port               = 443
      origin_protocol_policy   = "https-only"
      origin_ssl_protocols     = ["TLSv1.2"]
      origin_read_timeout      = 60 # CloudFront's default maximum
      origin_keepalive_timeout = 5
    }
  }

  default_cache_behavior {
    target_origin_id         = "lambda"
    viewer_protocol_policy   = "https-only"
    allowed_methods          = ["GET", "HEAD", "OPTIONS", "PUT", "POST", "PATCH", "DELETE"]
    cached_methods           = ["GET", "HEAD"]
    cache_policy_id          = data.aws_cloudfront_cache_policy.disabled.id
    origin_request_policy_id = data.aws_cloudfront_origin_request_policy.all_except_host.id
    compress                 = true
  }

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }

  viewer_certificate {
    acm_certificate_arn      = aws_acm_certificate_validation.this[0].certificate_arn
    ssl_support_method       = "sni-only"
    minimum_protocol_version = "TLSv1.2_2021"
  }
}
