package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type allBackupResponse struct {
	Objects map[string]VmBackup `json:"-"`
}

const (
	EnabledState  string = "Enabled"
	DisabledState string = "Disabled"
)

const (
	VMBackupReplication string = "VM"
	VMMirror            string = "Mirror"
	Metadata            string = "Metadata"
)

// This resource set type is used to allow differentiating between when a
// user wants to remove a resource set from a Backup (during an update) and when
// a resource set parameter should be omitted from a backup.set RPC call.
type FlatResourceSet struct {
	Id string
}

// This ensures when a FlatResourceSet is printed in debug logs
// that the string value of the Id is used rather than the pointer
// value. Since the purpose of this struct is to flatten resource
// sets to a string, it makes the logs properly reflect what is
// being logged.
func (rs *FlatResourceSet) String() string {
	return rs.Id
}

func (rs *FlatResourceSet) UnmarshalJSON(data []byte) (err error) {
	return json.Unmarshal(data, &rs.Id)
}

func (rs *FlatResourceSet) MarshalJSON() ([]byte, error) {
	if len(rs.Id) == 0 {
		var buf bytes.Buffer
		buf.WriteString(`null`)
		return buf.Bytes(), nil
	} else {
		return json.Marshal(rs.Id)
	}
}

type VmBackup struct {
	Vms      map[string]string `json:"vms"`
	Remotes  map[string]string `json:"remotes"`
	Type     string            `json:"type,omitempty"`
	Id       string            `json:"id,omitempty"`
	Settings map[string]string `json:"settings,omitempty"`
	Name     string            `json:"name"`
	Mode     string            `json:"mode"`
}

func (v VmBackup) Compare(obj interface{}) bool {
	other := obj.(VmBackup)
	if v.Id != "" && v.Id == other.Id {
		return true
	}

	if v.Name != "" && v.Name == other.Name {
		return true
	}

	return false
}

func (c *Client) CreateBackup(backupReq VmBackup, createTime time.Duration) (*VmBackup, error) {

	params := map[string]interface{}{
		"enabled":  false,
		"type":     backupReq.Type,
		"name":     backupReq.Name,
		"vms":      backupReq.Vms,
		"mode":     backupReq.Mode,
		"remotes":  backupReq.Remotes,
		"settings": backupReq.Settings,
	}

	log.Printf("[DEBUG] Backup params for backup.create %#v", params)
	var backupId string
	var err error
	err = c.Call("backup.create", params, &backupId)

	if err != nil {
		return nil, err
	}

	xsParams := map[string]interface{}{
		"id": backupId,
	}
	var success bool
	err = c.Call("backup.set", xsParams, &success)

	if err != nil {
		return nil, err
	}
	err = c.waitForModifyBackup(backupId, backupReq.PowerState, backupReq.WaitForIps, createTime)

	if err != nil {
		return nil, err
	}

	return c.GetBackup(
		VmBackup{
			Id: backupId,
		},
	)
}

func (c *Client) UpdateBackup(backupReq VmBackup) (*VmBackup, error) {
	params := map[string]interface{}{
		"id":       backupReq.Id,
		"enabled":  false,
		"type":     backupReq.Type,
		"name":     backupReq.Name,
		"vms":      backupReq.Vms,
		"mode":     backupReq.Mode,
		"remotes":  backupReq.Remotes,
		"settings": backupReq.Settings,
	}

	var success bool
	err := c.Call("backup.set", params, &success)

	if err != nil {
		return nil, err
	}

	// TODO: This is a poor way to ensure that terraform will see the updated
	// attributes after calling backup.set. Need to investigate a better way to detect this.
	time.Sleep(25 * time.Second)

	return c.GetBackup(backupReq)
}

func (c *Client) DeleteBackup(id string) error {
	params := map[string]interface{}{
		"id": id,
	}
	// Xen Orchestra versions >= 5.69.0 changed this return value to a bool
	// when older versions returned an object. This needs to be an interface
	// type in order to be backwards compatible while fixing this bug. See
	// GitHub issue 196 for more details.
	var reply interface{}
	return c.Call("backup.delete", params, &reply)
}

func (c *Client) GetBackup(backupReq VmBackup) (*VmBackup, error) {
	obj, err := c.FindFromGetAllObjects(backupReq)

	if err != nil {
		return nil, err
	}
	backups := obj.([]VmBackup)

	if len(backups) != 1 {
		return nil, errors.New(fmt.Sprintf("expected to find a single VmBackup from request %+v, instead found %d", backupReq, len(backups)))
	}

	log.Printf("[DEBUG] Found backup: %+v", backups[0])
	return &backups[0], nil
}

func (c *Client) GetBackups(backup VmBackup) ([]VmBackup, error) {
	obj, err := c.FindFromGetAllObjects(backup)
	if err != nil {
		return []VmBackup{}, err
	}
	backups := obj.([]VmBackup)
	log.Printf("[DEBUG] Found backups: %+v", backups)
	return backups, nil
}

func (c *Client) waitForBackupState(id string, stateConf StateChangeConf) error {
	stateConf.Refresh = GetBackupPowerState(c, id)
	_, err := stateConf.WaitForState()
	return err
}

func (c *Client) waitForModifyBackup(id string, desiredPowerState string, waitForIps map[string]string, timeout time.Duration) error {
	if len(waitForIps) == 0 {
		return waitForPowerStateReached(c, id, desiredPowerState, timeout)
	} else {
		return waitForIPAssignment(c, id, waitForIps, timeout)
	}
}

func FindOrCreateBackupForTests(backup *VmBackup, poolId, srId, templateName, tag string) {
	c, err := NewClient(GetConfigFromEnv())
	if err != nil {
		fmt.Printf("failed to create client with error: %v\n", err)
		os.Exit(-1)
	}

	var backupRes *VmBackup
	var net *Network
	backupRes, err = c.GetBackup(VmBackup{
		Tags: []string{tag},
	})

	if _, ok := err.(NotFound); ok {
		net, err = c.GetNetwork(Network{
			// We assume that a eth0 pool wide network exists
			// since trying to discern what the appropriate network
			// is from our current set of test inputs is challenging.
			// If this proves problematic then it can be reconsidered.
			NameLabel: "Pool-wide network associated with eth0",
			PoolId:    poolId,
		})

		if err != nil {
			fmt.Println("Failed to get network to create a VmBackup for the tests")
			os.Exit(-1)
		}

		backupRes, err = c.CreateBackup(
			VmBackup{
				NameLabel: fmt.Sprintf("Terraform testing - %d", time.Now().Unix()),
				Tags:      []string{tag},
				Template:  templateName,
				CPUs: CPUs{
					Number: 1,
				},
				Memory: MemoryObject{
					Static: []int{
						0, 2147483648,
					},
				},
				VIFsMap: []map[string]string{
					{
						"network": net.Id,
					},
				},
				Disks: []Disk{
					Disk{
						VDI: VDI{
							SrId:      srId,
							NameLabel: "terraform xenorchestra client testing",
							Size:      16106127360,
						},
					},
					Disk{
						VDI: VDI{
							SrId:      srId,
							NameLabel: "disk2",
							Size:      16106127360,
						},
					},
				},
			},
			5*time.Minute,
		)
	}

	if err != nil {
		fmt.Println(fmt.Sprintf("failed to find backup for the client tests with error: %v\n", err))
		os.Exit(-1)
	}

	*backup = *backupRes
}

func RemoveBackupsWithNamePrefix(prefix string) func(string) error {
	return func(_ string) error {
		fmt.Println("[DEBUG] Running backup sweeper")
		c, err := NewClient(GetConfigFromEnv())
		if err != nil {
			return fmt.Errorf("error getting client: %s", err)
		}

		var backupsMap map[string]VmBackup
		err = c.GetAllObjectsOfType(VmBackup{}, &backupsMap)
		if err != nil {
			return fmt.Errorf("error getting backups: %s", err)
		}
		for _, backup := range backupsMap {
			if strings.HasPrefix(backup.NameLabel, prefix) {
				if checkBlockDestroyOperation(&backup) {
					var success bool
					blockedOperations := map[string]interface{}{
						"destroy": nil,
					}
					params := map[string]interface{}{
						"id":                backup.Id,
						"blockedOperations": blockedOperations,
					}
					client, _ := c.(*Client)
					err := client.Call("backup.set", params, &success)

					if err != nil {
						log.Printf("error removing destroy block on backup `%s` during sweep: %s", backup.NameLabel, err)
					}
				}
				fmt.Printf("[DEBUG] Deleting backup `%s`\n", backup.NameLabel)
				err := c.DeleteBackup(backup.Id)
				if err != nil {
					log.Printf("error destroying backup `%s` during sweep: %s", backup.NameLabel, err)
				}
			}
		}
		return nil
	}
}

// This is not meant to be a robust check since it would be complicated to detect all
// malformed config. The goal is to cover what is supported by the cloudinit terraform
// provider (https://github.com/hashicorp/terraform-provider-cloudinit) and to rule out
// obviously bad config
func warnOnInvalidCloudConfig(cloudConfig string) {
	contentType := http.DetectContentType([]byte(cloudConfig))
	if contentType == "application/x-gzip" {
		return
	}

	if strings.HasPrefix(cloudConfig, "Content-Type") {
		if !strings.Contains(cloudConfig, "multipart/") {

			log.Printf("[WARNING] Detected MIME type that may not be supported by cloudinit")
			log.Printf("[WARNING] Validate that your configuration is well formed according to the documentation (https://cloudinit.readthedocs.io/en/latest/topics/format.html).\n")
		}
		return
	}
	if !strings.HasPrefix(cloudConfig, "#cloud-config") {
		log.Printf("[WARNING] cloud config does not start with required text `#cloud-config`.")
		log.Printf("[WARNING] Validate that your configuration is well formed according to the documentation (https://cloudinit.readthedocs.io/en/latest/topics/format.html).\n")
	}

}
