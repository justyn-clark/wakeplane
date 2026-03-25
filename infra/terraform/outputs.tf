output "zone_id" {
  description = "Route53 hosted zone ID"
  value       = aws_route53_zone.main.zone_id
}

output "name_servers" {
  description = "Name servers for the hosted zone (configure these at your domain registrar)"
  value       = aws_route53_zone.main.name_servers
}

output "apex_record" {
  description = "A record for apex domain"
  value       = aws_route53_record.apex.fqdn
}

output "www_record" {
  description = "CNAME record for www subdomain"
  value       = aws_route53_record.www.fqdn
}
