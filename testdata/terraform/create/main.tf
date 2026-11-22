terraform {
  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 6.0" }
  }
}
variable "region" {
  type    = string
  default = "us-east-1"
}
provider "aws" {
  region                      = var.region
  access_key                  = "mock"
  secret_key                  = "mock"
  skip_credentials_validation = true
  skip_requesting_account_id  = true
  skip_metadata_api_check     = true
}
provider "aws" {
  alias                       = "west"
  region                      = "us-west-2"
  access_key                  = "mock"
  secret_key                  = "mock"
  skip_credentials_validation = true
  skip_requesting_account_id  = true
  skip_metadata_api_check     = true
}
resource "aws_instance" "api" {
  count         = 2
  ami           = "ami-0123456789abcdef0"
  instance_type = "m6i.large"
  root_block_device {
    volume_size = 30
    volume_type = "gp3"
  }
}
resource "aws_ebs_volume" "data" {
  availability_zone = "us-east-1a"
  size              = 500
  type              = "gp3"
  iops              = 6000
  throughput        = 250
}
resource "aws_nat_gateway" "main" {
  allocation_id = "eipalloc-0123456789abcdef0"
  subnet_id     = "subnet-0123456789abcdef0"
}
resource "aws_lb" "web" {
  provider           = aws.west
  name               = "web"
  load_balancer_type = "application"
  subnets            = ["subnet-0123456789abcdef0", "subnet-0123456789abcdef1"]
}
resource "aws_eks_cluster" "main" {
  name     = "main"
  role_arn = "arn:aws:iam::123456789012:role/eks"
  version  = "1.33"
  vpc_config {
    subnet_ids = ["subnet-0123456789abcdef0", "subnet-0123456789abcdef1"]
  }
}
resource "aws_eks_node_group" "default" {
  cluster_name    = "main"
  node_group_name = "default"
  node_role_arn   = "arn:aws:iam::123456789012:role/node"
  subnet_ids      = ["subnet-0123456789abcdef0"]
  instance_types  = ["m6i.large"]
  scaling_config {
    desired_size = 3
    max_size     = 5
    min_size     = 1
  }
}
resource "aws_db_instance" "app" {
  identifier                  = "app"
  engine                      = "postgres"
  instance_class              = "db.m6g.large"
  allocated_storage           = 100
  storage_type                = "gp3"
  multi_az                    = true
  username                    = "app"
  manage_master_user_password = true
  skip_final_snapshot         = true
}
module "edge" {
  source    = "./edge"
  providers = { aws = aws.west }
}
resource "aws_s3_bucket" "logs" {
  bucket = "logs-example"
}
