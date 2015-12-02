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

func getInstanceGroupMemberUrls(d *schema.ResourceData, config *Config) ([]string, error) {
	var memberUrls []string
	members, err := config.clientCompute.InstanceGroups.ListInstances(
		config.Project, d.Get("zone").(string), d.Id(), &compute.InstanceGroupsListInstancesRequest{
			InstanceState: "ALL",
		}).Do()
	if err != nil {
		if gerr, ok := err.(*googleapi.Error); ok && gerr.Code == 404 {
			// The resource doesn't have any instances
			d.Set("instances", nil)
			return memberUrls, nil
		}

		return memberUrls, fmt.Errorf("Error reading InstanceGroup Members: %s", err)
	}

	for _, member := range members.Items {
		memberUrls = append(memberUrls, member.Instance)
	}
	return memberUrls, nil
}

func calcInstanceAddRemove(from []string, to []string, current []string) ([]string, []string) {
	add := make([]string, 0)
	remove := make([]string, 0)

	for _, u := range to {
		found := false
		for _, v := range from {
			if u == v {
				found = true
				break
			}
		}
		if !found {
			// sanity check not already present
			dupe := false
			for _, c := range current {
				if c == u {
					dupe = true
				}
			}
			if !dupe {
				add = append(add, u)
			}
		}
	}
	for _, u := range from {
		found := false
		for _, v := range to {
			if u == v {
				found = true
				break
			}
		}
		if !found {
			remove = append(remove, u)
		}
	}
	return add, remove
}

func resourceComputeInstanceGroupCreate(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*Config)

	// Build the parameter
	instanceGroup := &compute.InstanceGroup{
		Name: d.Get("name").(string),
	}

	// Set optional fields
	if v, ok := d.GetOk("description"); ok {
		instanceGroup.Description = v.(string)
	}

	if v, ok := d.GetOk("named_port"); ok {
		instanceGroup.NamedPorts = getNamedPorts(v.([]interface{}))
	}

	log.Printf("[DEBUG] InstanceGroup insert request: %#v", instanceGroup)
	op, err := config.clientCompute.InstanceGroups.Insert(
		config.Project, d.Get("zone").(string), instanceGroup).Do()
	if err != nil {
		return fmt.Errorf("Error creating InstanceGroup: %s", err)
	}

	// It probably maybe worked, so store the ID now
	d.SetId(instanceGroup.Name)

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

		addInstanceReq := &compute.InstanceGroupsAddInstancesRequest{
			Instances: getInstanceReferences(instanceUrls),
		}

		log.Printf("[DEBUG] InstanceGroup add instances request: %#v", addInstanceReq)
		op, err := config.clientCompute.InstanceGroups.AddInstances(
			config.Project, d.Get("zone").(string), d.Id(), addInstanceReq).Do()
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

	// retreive instance group
	instanceGroup, err := config.clientCompute.InstanceGroups.Get(
		config.Project, d.Get("zone").(string), d.Id()).Do()
	if err != nil {
		if gerr, ok := err.(*googleapi.Error); ok && gerr.Code == 404 {
			// The resource doesn't exist anymore
			d.SetId("")

			return nil
		}

		return fmt.Errorf("Error reading InstanceGroup: %s", err)
	}
	// retreive instance group members
	memberUrls, err := getInstanceGroupMemberUrls(d, config)
	if err != nil {
		return err
	}

	// check for manually removed instances we care about and update local state
	if v, ok := d.GetOk("instances"); ok {
		// get the instance urls present in the state
		stateUrls, err := convertInstances(config, convertStringArr(v.([]interface{})))
		if err != nil {
			return err
		}
		// update the state with any manually removed instances that are defined
		var liveMembers []string
		for _, s := range stateUrls {
			// for each instance in state check it actually exists
			for _, r := range memberUrls {
				if r == s {
					liveMembers = append(liveMembers, r)
					break
				}
			}
		}
		d.Set("instances", realMembers)
	}

	// Set computed fields
	d.Set("network", instanceGroup.Network)
	d.Set("size", instanceGroup.Size)
	d.Set("self_link", instanceGroup.SelfLink)

	return nil
}

func resourceComputeInstanceGroupUpdate(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*Config)

	// refresh the state incase referenced instances have been removed in the run
	err := resourceComputeInstanceGroupRead(d, meta)
	if err != nil {
		return fmt.Errorf("Error reading InstanceGroup: %s", err)
	}

	d.Partial(true)

	if d.HasChange("instances") {
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
		//  current live members
		liveUrls, err := getInstanceGroupMemberUrls(d, config)

		add, remove := calcInstanceAddRemove(fromUrls, toUrls, liveUrls)

		if len(remove) > 0 {
			removeReq := &compute.InstanceGroupsRemoveInstancesRequest{
				Instances: getInstanceReferences(remove),
			}

			log.Printf("[DEBUG] InstanceGroup remove instances request: %#v", removeReq)
			removeOp, err := config.clientCompute.InstanceGroups.RemoveInstances(
				config.Project, d.Get("zone").(string), d.Id(), removeReq).Do()
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

			addReq := &compute.InstanceGroupsAddInstancesRequest{
				Instances: getInstanceReferences(add),
			}

			log.Printf("[DEBUG] InstanceGroup adding instances request: %#v", addReq)
			addOp, err := config.clientCompute.InstanceGroups.AddInstances(
				config.Project, d.Get("zone").(string), d.Id(), addReq).Do()
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

		namedPortsReq := &compute.InstanceGroupsSetNamedPortsRequest{
			Fingerprint: d.Get("fingerprint").(string),
			NamedPorts:  namedPorts,
		}

		log.Printf("[DEBUG] InstanceGroup updating named ports request: %#v", namedPortsReq)
		op, err := config.clientCompute.InstanceGroups.SetNamedPorts(
			config.Project, d.Get("zone").(string), d.Id(), namedPortsReq).Do()
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
