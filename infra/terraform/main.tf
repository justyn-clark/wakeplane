resource "aws_route53_zone" "main" {
  name = var.domain

  tags = {
    Name      = var.domain
    ManagedBy = "terraform"
  }
}

resource "aws_route53_record" "apex" {
  zone_id = aws_route53_zone.main.zone_id
  name    = var.domain
  type    = "A"
  ttl     = 300
  records = ["216.198.79.1"]
}

resource "aws_route53_record" "www" {
  zone_id = aws_route53_zone.main.zone_id
  name    = "www.${var.domain}"
  type    = "CNAME"
  ttl     = 300
  # Update this with the CNAME value shown in Vercel after the domain is linked.
  records = ["cname.vercel-dns.com."]
}
