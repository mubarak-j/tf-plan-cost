<!-- tf-plan-cost -->
### tf-plan-cost: **+$242.24/mo** (was $323.57 → now $565.81)

| Resource | Action | Before | After | Δ/mo |
|---|---|---|---|---|
| `aws_eks_node_group.default` | ~ 3 × m6i.large → 5 × m6i.large | $210.24 | $350.40 | +$140.16 |
| `aws_instance.api[0]` | ~ m6i.large → m6i.xlarge | $72.48 | $142.56 | +$70.08 |
| `aws_ebs_volume.data` | ~ gp3 100 GB → gp3 500 GB | $8.00 | $40.00 | +$32.00 |

<details><summary>Notes (2)</summary>

- `aws_eks_node_group.default`: node root volumes not included
- `aws_eks_node_group.default`: priced as m6i.large; other instance types (m5.large) may cost more or less

</details>

<details><summary>Not priced (1)</summary>

| Resource | Action | Reason |
|---|---|---|
| `aws_s3_bucket.logs` | update | resource type not supported yet |

</details>

<sub>Estimated with on-demand Linux prices and 730 hours/month from the AWS Price List API (prices as of 2026-11-07). Usage-based charges such as data transfer, requests and LCUs are not included. 1 unchanged resource ($32.85/mo) not shown.</sub>
