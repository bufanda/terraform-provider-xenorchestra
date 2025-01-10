package xoa

import (
	"log"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/vatesfr/terraform-provider-xenorchestra/client"
	"github.com/vatesfr/terraform-provider-xenorchestra/xoa/internal"
)

func dataSourceXoaBackups() *schema.Resource {

	return &schema.Resource{
		Description: "Use this data source to filter Xenorchestra Backupss by certain criteria (backup_id, name or mode) for use in other resources.",
		Read:        dataSourceBackupsRead,
		Schema: map[string]*schema.Schema{
			"backups": &schema.Schema{
				Type:        schema.TypeList,
				Computed:    true,
				Elem:        resourceBackup(),
				Description: "A list of information for all backups found in this pool.",
			},
			"pool_id": &schema.Schema{
				Type:        schema.TypeString,
				Description: "The ID of the pool the VM belongs to.",
				Required:    true,
			},
			"host": &schema.Schema{
				Type:     schema.TypeString,
				Optional: true,
			},
			"power_state": &schema.Schema{
				Type:        schema.TypeString,
				Description: "The power state of the backups. (Running, Halted)",
				Optional:    true,
			},
		},
	}
}

func dataSourceBackupsRead(d *schema.ResourceData, m interface{}) error {
	c := m.(client.XOClient)
	searchBackup := client.Backup{
		PowerState: d.Get("power_state").(string),
		Host:       d.Get("host").(string),
		PoolId:     d.Get("pool_id").(string),
	}

	backups, err := c.GetBackups(searchBackup)
	if err != nil {
		return err
	}

	if err = d.Set("backups", backupToMapList(backups)); err != nil {
		return err
	}
	d.SetId(internal.Strings([]string{searchBackup.PowerState, searchBackup.PoolId, searchBackup.Host}))
	return nil

}

func backupToMapList(backups []client.Backup) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(backups))
	for _, backup := range backups {
		log.Printf("[DEBUG] IPS %s\n", backup.Addresses)
		var ipv4 []string
		var ipv6 []string
		for key, address := range backup.Addresses {
			if strings.Contains(key, "ipv4") {
				ipv4 = append(ipv4, address)
			} else if strings.Contains(key, "ipv6") {
				ipv6 = append(ipv6, address)
			}
		}
		log.Printf("[DEBUG] VBD on %s (%s) %s\n", backup.VBDs, backup.NameLabel, backup.Id)
		hostMap := map[string]interface{}{
			"id":                   backup.Id,
			"name_label":           backup.NameLabel,
			"cpus":                 backup.CPUs.Number,
			"cloud_config":         backup.CloudConfig,
			"cloud_network_config": backup.CloudNetworkConfig,
			"tags":                 backup.Tags,
			"memory_max":           backup.Memory.Static[1],
			"affinity_host":        backup.AffinityHost,
			"template":             backup.Template,
			"high_availability":    backup.HA,
			"ipv4_addresses":       ipv4,
			"ipv6_addresses":       ipv6,
			"power_state":          backup.PowerState,
			"host":                 backup.Host,
			"auto_poweron":         backup.AutoPoweron,
			"name_description":     backup.NameDescription,
		}
		if backup.ResourceSet != nil {
			hostMap["resource_set"] = backup.ResourceSet.Id
		}
		result = append(result, hostMap)
	}

	return result
}
