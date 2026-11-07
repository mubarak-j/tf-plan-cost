<!-- tf-plan-cost -->
### tf-plan-cost: **+$111.68/mo** (was $8.23 → now $119.91)

> [!WARNING]
> 3 changed resources could not be priced, so the real change may differ. See **Not priced** below.

| Resource | Action | Before | After | Δ/mo |
|---|---|---|---|---|
| `module.app["blue"].aws_instance.api[0]` | + m6i.large | – | $71.68 | +$71.68 |
| `aws_ebs_volume.data` | + gp3 500 GB | – | $40.00 | +$40.00 |

<details><summary>Not priced (4)</summary>

| Resource | Action | Reason |
|---|---|---|
| `aws_instance.spot` | create | spot instances are not priced (on-demand only) |
| `aws_instance.dynamic` | create | instance_type is known only after apply |
| `aws_db_instance.legacy` | create | engine "oracle-ee" is not supported yet (supported: postgres, mysql, mariadb) |
| `aws_sqs_queue.jobs` | create | resource type not supported yet |

</details>

<sub>Estimated with on-demand Linux prices and 730 hours/month from the AWS Price List API (prices as of 2026-11-07). Usage-based charges such as data transfer, requests and LCUs are not included. 1 unchanged resource ($8.23/mo) not shown.</sub>
