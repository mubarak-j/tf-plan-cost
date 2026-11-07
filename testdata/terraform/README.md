# Fixture Terraform

`create/` produces `../plans/create.json`. It uses mock credentials and only
creates resources, so `terraform plan` makes no AWS API calls:

```sh
cd testdata/terraform/create
terraform init && terraform plan -out=tfplan && terraform show -json tfplan > ../../plans/create.json
rm -rf .terraform .terraform.lock.hcl tfplan
```

The other plans under `../plans/` are handwritten in the same format to cover
updates, deletes, replacements and edge cases.
