<!-- tf-plan-cost -->
### tf-plan-cost: **-$122.28/mo** (was $122.28 → now $0.00)

| Resource | Action | Before | After | Δ/mo |
|---|---|---|---|---|
| `aws_eks_cluster.old` | - EKS cluster v1.31 | $73.00 | – | -$73.00 |
| `aws_nat_gateway.main` | - NAT gateway (public) | $32.85 | – | -$32.85 |
| `aws_lb.internal` | - Network Load Balancer | $16.43 | – | -$16.43 |

<details><summary>Notes (2)</summary>

- `aws_nat_gateway.main`: data processing at $0.045/GB not included
- `aws_lb.internal`: capacity units (LCU) at $0.006/LCU-Hrs not included

</details>

<sub>Estimated with on-demand Linux prices and 730 hours/month from the AWS Price List API (prices as of 2026-11-07). Usage-based charges such as data transfer, requests and LCUs are not included.</sub>
