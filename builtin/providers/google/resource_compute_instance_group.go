package google

import (
	"fmt"
	"log"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"

	"github.com/hashicorp/terraform/helper/schema"
)

func resourceComputeInstanceGroup() *schema.Resource {
	return &schema.Resource{
		Create: resourceComputeInstanceGroupCreate,
		Read:   resourceComputeInstanceGroupRead,
		Update: resourceComputeInstanceGroupUpdate,
		Delete: resourceComputeInstanceGroupDelete,

		Schema: map[string]*schema.Schema{
			"name": &schema.Schema{
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},

			"description": &schema.Schema{
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
			},

			"fingerprint": &schema.Schema{
				Type:     schema.TypeString,
				Computed: true,
			},

			"named_port": &schema.Schema{
				Type:     schema.TypeList,
				Optional: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"name": &schema.Schema{
							Type:     schema.TypeString,
							Required: true,
						},

						"port": &schema.Schema{
							Type:     schema.TypeInt,
							Required: true,
						},
					},
				},
			},

			"instances": &schema.Schema{
				Type:     schema.TypeList,
				Optional: true,
				ForceNew: false,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},

			"network": &schema.Schema{
				Type:     schema.TypeString,
				Computed: true,
			},

			"size": &schema.Schema{
				Type:     schema.TypeInt,
				Computed: true,
			},

			"zone": &schema.Schema{
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},

			"self_link": &schema.Schema{
				Type:     schema.TypeString,
				Computed: true,
			},
		},
	}
}

func getNamedPorts(nps []interface{}) []*compute.NamedPort {
	namedPorts := make([]*compute.NamedPort, 0, len(nps))
	for _, v := range nps {
		np := v.(map[string]interface{})
		namedPorts = append(namedPorts, &compute.NamedPort{
			Name: np["name"].(string),
			Port: int64(np["port"].(int)),
		})

	}
	return namedPorts
}

func getInstanceReferences(instanceUrls []string) (refs []*compute.InstanceReference) {
	for _, v := range instanceUrls {
		refs = append(refs, &compute.InstanceReference{
			Instance: v,
		})
	}
	return refs
}

func resourceComputeInstanceGroupCreate(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*Config)

	// Build the parameter
	manager := &compute.InstanceGroup{
		Name: d.Get("name").(string),
	}

	// Set optional fields
	if v, ok := d.GetOk("description"); ok {
		manager.Description = v.(string)
	}

	if v, ok := d.GetOk("named_port"); ok {
		manager.NamedPorts = getNamedPorts(v.([]interface{}))
	}

	log.Printf("[DEBUG] InstanceGroup insert request: %#v", manager)
	op, err := config.clientCompute.InstanceGroups.Insert(
		config.Project, d.Get("zone").(string), manager).Do()
	if err != nil {
		return fmt.Errorf("Error creating InstanceGroup: %s", err)
	}

	// It probably maybe worked, so store the ID now
	d.SetId(manager.Name)

	// Wait for the operation to complete
	err = computeOperationWaitZone(config, op, d.Get("zone").(string), "Creating InstanceGroup")
	if err != nil {
		return err
	}

	if v, ok := d.GetOk("instances"); ok {
		instanceUrls, err := convertInstances(config, convertStringArr(v.([]interface{})))
		if err != nil {
			return err
		}

		instanceManager := &compute.InstanceGroupsAddInstancesRequest{
			Instances: getInstanceReferences(instanceUrls),
		}

		log.Printf("[DEBUG] InstanceGroup add instances request: %#v", manager)
		op, err := config.clientCompute.InstanceGroups.AddInstances(
			config.Project, d.Get("zone").(string), d.Id(), instanceManager).Do()
		if err != nil {
			return fmt.Errorf("Error adding instances to InstanceGroup: %s", err)
		}

		// Wait for the operation to complete
		err = computeOperationWaitZone(config, op, d.Get("zone").(string), "Adding instances to InstanceGroup")
		if err != nil {
			return err
		}
	}

	return resourceComputeInstanceGroupRead(d, meta)
}

func resourceComputeInstanceGroupRead(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*Config)

	manager, err := config.clientCompute.InstanceGroups.Get(
		config.Project, d.Get("zone").(string), d.Id()).Do()
	if err != nil {
		if gerr, ok := err.(*googleapi.Error); ok && gerr.Code == 404 {
			// The resource doesn't exist anymore
			d.SetId("")

			return nil
		}

		return fmt.Errorf("Error reading InstanceGroup: %s", err)
	}

	// Set computed fields
	d.Set("fingerprint", manager.Fingerprint)
	d.Set("network", manager.Network)
	d.Set("size", manager.Size)
	d.Set("self_link", manager.SelfLink)

	return nil
}
func resourceComputeInstanceGroupUpdate(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*Config)

	d.Partial(true)

	if d.HasChange("instances") {
		// to-do check for no instances
		from_, to_ := d.GetChange("instances")

		from := convertStringArr(from_.([]interface{}))
		to := convertStringArr(to_.([]interface{}))

		fromUrls, err := convertInstances(config, from)
		if err != nil {
			return err
		}
		toUrls, err := convertInstances(config, to)
		if err != nil {
			return err
		}

		add, remove := calcAddRemove(fromUrls, toUrls)

		if len(remove) > 0 {
			removeManager := &compute.InstanceGroupsRemoveInstancesRequest{
				Instances: getInstanceReferences(remove),
			}

			log.Printf("[DEBUG] InstanceGroup remove instances request: %#v", removeManager)
			removeOp, err := config.clientCompute.InstanceGroups.RemoveInstances(
				config.Project, d.Get("zone").(string), d.Id(), removeManager).Do()
			if err != nil {
				return fmt.Errorf("Error removing instances from InstanceGroup: %s", err)
			}

			// Wait for the operation to complete
			err = computeOperationWaitZone(config, removeOp, d.Get("zone").(string), "Updating InstanceGroup")
			if err != nil {
				return err
			}
		}

		if len(add) > 0 {

			addManager := &compute.InstanceGroupsAddInstancesRequest{
				Instances: getInstanceReferences(add),
			}

			log.Printf("[DEBUG] InstanceGroup adding instances request: %#v", addManager)
			addOp, err := config.clientCompute.InstanceGroups.AddInstances(
				config.Project, d.Get("zone").(string), d.Id(), addManager).Do()
			if err != nil {
				return fmt.Errorf("Error adding instances from InstanceGroup: %s", err)
			}

			// Wait for the operation to complete
			err = computeOperationWaitZone(config, addOp, d.Get("zone").(string), "Updating InstanceGroup")
			if err != nil {
				return err
			}
		}

		d.SetPartial("instances")
	}

	if d.HasChange("named_port") {
		namedPorts := getNamedPorts(d.Get("named_port").([]interface{}))

		manager := &compute.InstanceGroupsSetNamedPortsRequest{
			Fingerprint: d.Get("fingerprint").(string),
			NamedPorts:  namedPorts,
		}

		log.Printf("[DEBUG] InstanceGroup updating named ports request: %#v", manager)
		op, err := config.clientCompute.InstanceGroups.SetNamedPorts(
			config.Project, d.Get("zone").(string), d.Id(), manager).Do()
		if err != nil {
			return fmt.Errorf("Error updating named ports for InstanceGroup: %s", err)
		}

		err = computeOperationWaitZone(config, op, d.Get("zone").(string), "Updating InstanceGroup")
		if err != nil {
			return err
		}
		d.SetPartial("named_port")
	}

	d.Partial(false)

	return resourceComputeInstanceGroupRead(d, meta)
}

func resourceComputeInstanceGroupDelete(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*Config)

	zone := d.Get("zone").(string)
	op, err := config.clientCompute.InstanceGroups.Delete(config.Project, zone, d.Id()).Do()
	if err != nil {
		return fmt.Errorf("Error deleting InstanceGroup: %s", err)
	}

	err = computeOperationWaitZone(config, op, zone, "Deleting InstanceGroup")
	if err != nil {
		return err
	}

	d.SetId("")
	return nil
}
