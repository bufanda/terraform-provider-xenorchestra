# xenorchestra_template

Provides information about a VM template that can be used for creating new VMs.

## Example Usage

```hcl
data "xenorchestra_template" "template" {
  name_label = "Ubuntu Bionic Beaver 18.04"
}

resource "xenorchestra_vm" "demo-vm" {
  // ...
  template = data.xenorchestra_template.template.id
  // ...
}
```

## Argument Reference
* name_label - (Required) The name of the template you want to look up.

## Attributes Reference
* id is set to the ID generated by the XO api.
* uuid - uuid of the template.