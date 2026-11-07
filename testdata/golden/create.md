<!-- tf-plan-cost -->
### tf-plan-cost: **+$825.47/mo** (was $0.00 → now $825.47)

| Resource | Action | Before | After | Δ/mo |
|---|---|---|---|---|
| `aws_db_instance.app` | + db.m6g.large PostgreSQL Multi-AZ, gp3 100 GB | – | $255.14 | +$255.14 |
| `aws_eks_node_group.default` | + 3 × m6i.large | – | $210.24 | +$210.24 |
| `aws_eks_cluster.main` | + EKS cluster v1.33 | – | $73.00 | +$73.00 |
| `aws_instance.api[0]` | + m6i.large | – | $72.48 | +$72.48 |
| `aws_instance.api[1]` | + m6i.large | – | $72.48 | +$72.48 |
| `aws_ebs_volume.data` | + gp3 500 GB | – | $60.00 | +$60.00 |
| `aws_nat_gateway.main` | + NAT gateway (public) | – | $32.85 | +$32.85 |
| `module.edge.aws_nat_gateway.edge` | + NAT gateway (public) | – | $32.85 | +$32.85 |
| `aws_lb.web` | + Application Load Balancer | – | $16.43 | +$16.43 |

<details><summary>Notes (5)</summary>

- `aws_eks_node_group.default`: node root volumes not included
- `aws_eks_cluster.main`: extended support ($0.50/hr, $365.00/mo) applies instead once the Kubernetes version leaves standard support
- `aws_nat_gateway.main`: data processing at $0.045/GB not included
- `module.edge.aws_nat_gateway.edge`: data processing at $0.045/GB not included
- `aws_lb.web`: capacity units (LCU) at $0.008/LCU-Hrs not included

</details>

<details><summary>Not priced (1)</summary>

| Resource | Action | Reason |
|---|---|---|
| `aws_s3_bucket.logs` | create | resource type not supported yet |

</details>

<sub>Estimated with on-demand Linux prices and 730 hours/month from the AWS Price List API (prices as of 2026-11-07). Usage-based charges such as data transfer, requests and LCUs are not included.</sub>
