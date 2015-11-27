package google

import (
	"fmt"
	"testing"

	"google.golang.org/api/compute/v1"

	"github.com/hashicorp/terraform/helper/resource"
	"github.com/hashicorp/terraform/terraform"
)

func TestAccInstanceGroup_basic(t *testing.T) {
	var manager compute.InstanceGroup

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckInstanceGroupDestroy,
		Steps: []resource.TestStep{
			resource.TestStep{
				Config: testAccInstanceGroup_basic,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckInstanceGroupExists(
						"google_compute_instance_group.ig-basic", &manager),
					testAccCheckInstanceGroupExists(
						"google_compute_instance_group.ig-empty", &manager),
				),
			},
		},
	})
}

func TestAccInstanceGroup_update(t *testing.T) {
	var manager compute.InstanceGroup

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckInstanceGroupDestroy,
		Steps: []resource.TestStep{
			resource.TestStep{
				Config: testAccInstanceGroup_update,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckInstanceGroupExists(
						"google_compute_instance_group.ig-update", &manager),
					testAccCheckInstanceGroupNamedPorts(
						"google_compute_instance_group.ig-update",
						map[string]int64{"http": 8080, "https": 8443},
						&manager),
				),
			},
			resource.TestStep{
				Config: testAccInstanceGroup_update2,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckInstanceGroupExists(
						"google_compute_instance_group.ig-update", &manager),
					testAccCheckInstanceGroupUpdated(
						"google_compute_instance_group.ig-update", 3, &manager),
					testAccCheckInstanceGroupNamedPorts(
						"google_compute_instance_group.ig-update",
						map[string]int64{"http": 8081, "test": 8444},
						&manager),
				),
			},
		},
	})
}

func testAccCheckInstanceGroupDestroy(s *terraform.State) error {
	config := testAccProvider.Meta().(*Config)

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "google_compute_instance_group_manager" {
			continue
		}
		_, err := config.clientCompute.InstanceGroups.Get(
			config.Project, rs.Primary.Attributes["zone"], rs.Primary.ID).Do()
		if err != nil {
			return fmt.Errorf("InstanceGroup still exists")
		}
	}

	return nil
}

func testAccCheckInstanceGroupExists(n string, manager *compute.InstanceGroup) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Not found: %s", n)
		}

		if rs.Primary.ID == "" {
			return fmt.Errorf("No ID is set")
		}

		config := testAccProvider.Meta().(*Config)

		found, err := config.clientCompute.InstanceGroups.Get(
			config.Project, rs.Primary.Attributes["zone"], rs.Primary.ID).Do()
		if err != nil {
			return err
		}

		if found.Name != rs.Primary.ID {
			return fmt.Errorf("InstanceGroup not found")
		}

		*manager = *found

		return nil
	}
}

func testAccCheckInstanceGroupUpdated(n string, size int64, manager *compute.InstanceGroup) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Not found: %s", n)
		}

		if rs.Primary.ID == "" {
			return fmt.Errorf("No ID is set")
		}

		config := testAccProvider.Meta().(*Config)

		manager, err := config.clientCompute.InstanceGroups.Get(
			config.Project, rs.Primary.Attributes["zone"], rs.Primary.ID).Do()
		if err != nil {
			return err
		}

		// Cannot check the target pool as the instance creation is asynchronous.  However, can
		// check the target_size.
		if manager.Size != size {
			return fmt.Errorf("instance count incorrect")
		}

		return nil
	}
}

func testAccCheckInstanceGroupNamedPorts(n string, np map[string]int64, manager *compute.InstanceGroup) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Not found: %s", n)
		}

		if rs.Primary.ID == "" {
			return fmt.Errorf("No ID is set")
		}

		config := testAccProvider.Meta().(*Config)

		manager, err := config.clientCompute.InstanceGroups.Get(
			config.Project, rs.Primary.Attributes["zone"], rs.Primary.ID).Do()
		if err != nil {
			return err
		}

		var found bool
		for _, namedPort := range manager.NamedPorts {
			found = false
			for name, port := range np {
				if namedPort.Name == name && namedPort.Port == port {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("named port incorrect")
			}
		}

		return nil
	}
}

const testAccInstanceGroup_basic = `
resource "google_compute_instance" "ig_instance" {
	name = "terraform-test-ig"
	machine_type = "n1-standard-1"
	can_ip_forward = false

	disk {
		image = "debian-7-wheezy-v20140814"
	}

	network_interface {
		network = "default"
	}
}

resource "google_compute_instance_group" "ig-basic" {
	description = "Terraform test instance group"
	name = "terraform-test-ig-basic"
	zone = "us-central1-c"
	instances = [ ${google_compute_instance.ig_instance.self_link} ]
	named_port {
		name = "http"
		port = "8080"
	}
	named_port {
		name = "https"
		port = "8443"
	}
}

resource "google_compute_instance_group" "ig-empty" {
	description = "Terraform test instance group empty"
	name = "terraform-test-ig-empty"
	zone = "us-central1-c"
		named_port {
		name = "http"
		port = "8080"
	}
	named_port {
		name = "https"
		port = "8443"
	}
}
`

const testAccInstanceGroup_update = `
resource "google_compute_instance" "ig_instance" {
	name = "terraform-test-ig-${count.index + 1}"
	machine_type = "n1-standard-1"
	can_ip_forward = false
	count = 1

	disk {
		image = "debian-7-wheezy-v20140814"
	}

	network_interface {
		network = "default"
	}
}

resource "google_compute_instance_group" "ig-update" {
	description = "Terraform test instance group"
	name = "terraform-test-ig-basic"
	zone = "us-central1-c"
	instances = [ ${google_compute_instance.ig_instance.*.self_link} ]
	named_port {
		name = "http"
		port = "8080"
	}
	named_port {
		name = "https"
		port = "8443"
	}
}
`

// Change IGM's instance template and target size
const testAccInstanceGroup_update2 = `
resource "google_compute_instance" "ig_instance" {
	name = "terraform-test-ig-${count.index + 1}"
	machine_type = "n1-standard-1"
	can_ip_forward = false
	count = 2

	disk {
		image = "debian-7-wheezy-v20140814"
	}

	network_interface {
		network = "default"
	}
}

resource "google_compute_instance_group" "ig-update" {
	description = "Terraform test instance group"
	name = "terraform-test-ig-basic"
	zone = "us-central1-c"
	instances = [ ${google_compute_instance.ig_instance.*.self_link} ]

	named_port {
		name = "http"
		port = "8081"
	}
	named_port {
		name = "test"
		port = "8444"
	}
}
`
