terraform {
  required_providers {
    aws = { source = "hashicorp/aws" }
  }
}
resource "aws_nat_gateway" "edge" {
  allocation_id = "eipalloc-0123456789abcdef1"
  subnet_id     = "subnet-0123456789abcdef1"
}
