variable "aws_region" {
  description = "AWS region (Route53 is global but provider requires a region)"
  type        = string
  default     = "us-east-1"
}

variable "domain" {
  description = "Domain name for the Route53 hosted zone"
  type        = string
  default     = "wakeplane.dev"
}
