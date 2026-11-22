# A small web stack to try tf-plan-cost. It needs no AWS account: the
# provider uses mock credentials, and because every resource is new,
# `terraform plan` makes no AWS API calls. Never `terraform apply` it.
#
# Try it: open a pull request that changes a size below (for example
# web_instance_type = "m6i.xlarge" or db_storage_gb = 200) and the
# tf-plan-cost comment shows what the change costs per month.

terraform {
  required_version = ">= 1.6"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

provider "aws" {
  region                      = var.region
  access_key                  = "mock"
  secret_key                  = "mock"
  skip_credentials_validation = true
  skip_requesting_account_id  = true
  skip_metadata_api_check     = true
}

variable "region" {
  type    = string
  default = "us-east-1"
}

variable "web_instance_type" {
  type    = string
  default = "m6i.large"
}

variable "web_count" {
  type    = number
  default = 2
}

variable "db_instance_class" {
  type    = string
  default = "db.m6g.large"
}

variable "db_storage_gb" {
  type    = number
  default = 100
}

# Placeholder network IDs: plan-only, nothing is looked up.
locals {
  subnet_ids = ["subnet-0aaaaaaaaaaaaaaaa", "subnet-0bbbbbbbbbbbbbbbb"]
}

resource "aws_instance" "web" {
  count         = var.web_count
  ami           = "ami-0123456789abcdef0"
  instance_type = var.web_instance_type
  subnet_id     = local.subnet_ids[count.index % 2]

  root_block_device {
    volume_size = 30
    volume_type = "gp3"
  }

  tags = { Name = "demo-web-${count.index}" }
}

resource "aws_lb" "web" {
  name               = "demo-web"
  load_balancer_type = "application"
  subnets            = local.subnet_ids
}

resource "aws_db_instance" "app" {
  identifier                  = "demo-app"
  engine                      = "postgres"
  instance_class              = var.db_instance_class
  allocated_storage           = var.db_storage_gb
  storage_type                = "gp3"
  multi_az                    = false
  username                    = "app"
  manage_master_user_password = true
  skip_final_snapshot         = true
}

resource "aws_ebs_volume" "uploads" {
  availability_zone = "us-east-1a"
  size              = 200
  type              = "gp3"
}

resource "aws_nat_gateway" "main" {
  subnet_id     = local.subnet_ids[0]
  allocation_id = "eipalloc-0123456789abcdef0"
}

resource "aws_s3_bucket" "assets" {
  bucket = "tf-plan-cost-demo-assets"
}
