<!-- tf-plan-cost -->
### tf-plan-cost: **+$3,873.97/mo** (was $422.36 → now $4,296.33)

| Resource | Action | Before | After | Δ/mo |
|---|---|---|---|---|
| `aws_ebs_volume.fast` | -/+ io1 100 GB → io2 200 GB | $337.50 | $3,752.10 | +$3,414.60 |
| `aws_db_instance.app` | -/+ db.t4g.micro MySQL, gp2 20 GB → db.m6g.large PostgreSQL Multi-AZ, gp3 500 GB | $13.98 | $467.14 | +$453.16 |
| `aws_instance.web` | -/+ m5.large → m6i.large | $70.88 | $77.09 | +$6.21 |

<details><summary>Notes (1)</summary>

- `aws_instance.web`: root volume not priced: root_block_device is known only after apply

</details>

<sub>Estimated with on-demand Linux prices and 730 hours/month from the AWS Price List API (prices as of 2026-11-07). Usage-based charges such as data transfer, requests and LCUs are not included.</sub>
