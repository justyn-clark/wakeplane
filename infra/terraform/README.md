# Terraform Infrastructure for wakeplane.dev

Route53 hosted zone configuration for `wakeplane.dev`.

This follows the same infrastructure pattern established for `smallprotocol.dev` and `musketeer.dev`.

## Pattern

- AWS Route53 managed zone for the domain
- Apex A record pointing to Vercel ingress (`216.198.79.1`)
- `www` CNAME pointing to Vercel DNS
- Vercel handles TLS and deployment; this Terraform scope is DNS only

The Vercel project itself is connected via the Vercel dashboard or CLI. This Terraform does not manage Vercel project creation or deployment (consistent with the existing JCN site pattern).

## Prerequisites

- Terraform >= 1.5
- AWS credentials with Route53 permissions (`route53:CreateHostedZone`, `route53:ChangeResourceRecordSets`, `route53:GetHostedZone`, `route53:ListHostedZones`)

## Usage

```bash
cd infra/terraform

terraform init
terraform plan
terraform apply
```

After apply, update your domain registrar with the name servers from the output:

```bash
terraform output name_servers
```

## Variables

| Variable | Default | Description |
|---|---|---|
| `domain` | `wakeplane.dev` | Domain name for the hosted zone |
| `aws_region` | `us-east-1` | AWS region (Route53 is global; region is required by provider) |

## After DNS propagates

1. Go to [Vercel dashboard](https://vercel.com) and add `wakeplane.dev` to your project.
2. Vercel will verify the apex A record and `www` CNAME automatically.
3. SSL is provisioned by Vercel after DNS verification.

Update `main.tf` `www` CNAME value with the actual CNAME target shown in the Vercel domains panel after linking the project. The initial value (`cname.vercel-dns.com.`) is a Vercel fallback; production deployments show a project-specific CNAME.

## Deployment

Deployments are triggered by Vercel's Git integration (push to `main`). To deploy manually:

```bash
# From the site repo (wakeplane.dev)
npm run build
npx vercel --prod
```

## Destroying

```bash
terraform destroy
```

This removes the Route53 hosted zone and records. Your domain registration at the registrar is not affected.
