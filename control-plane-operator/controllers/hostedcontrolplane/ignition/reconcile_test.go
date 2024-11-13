package ignition

import (
	"testing"

	"github.com/openshift/hypershift/support/testutil"

	k8syaml "sigs.k8s.io/yaml"
)

func TestWorkerSSHConfig(t *testing.T) {
	sshKey := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAACAQC7xaGqJaFd51jCl+MjZzgH1WfgKbNmn+AbvRXOabeNYNRTZiRNcFlWHQPxL/fFWiJ5rDkyTRm6dI49TflU5lMSOcKwoO0sZlMbrDrUeDf2cy/7KffpAto+Te8vB4udAERMJHY89v9/RF6GgMLpW+lbIT3Gyj+MbIF8aAz0vt6VJA8Ptwq2SlxWSPLbxoe5nNP1JaOubG4Arm6t75smJ+wvexV8d9duvFWig2MW5lMTAa6QpSAp6Gd03dWSUiH5++dk3vlNMR9hZMv7/DWqyauGi0MYtuywQqVWr3YMQve72VJTo/qVhvfFylKEFTKA0h5Cl3ziL0DbgM/RDsUqaLynB7b6jAJkhXd02wv6+IkHly02SEnLHGJs50uK7J7GdAWWbKfRByVGg5kP5DwiTEln357ukT7OH8Ys6PNd0Lzzy/oA4Gv+uDzI1RMMBsTcv3SwASuht+EZzQ5hoSCkM6QoEtpruSCEdCtvTEq9idcrVijKbYURtrDdH5WAN9ZYUF13s94870srbG3uavvT2G1IcWjBjiVVoJM8cifYnTHllHX/oPw9iZxhjlrC5Uc+dgRhnpoRYMar30Kg/No1GYj2EPEZgvHVde6KqActTFnD0K5xJEAUzKutu7TDUePm+MYREt4HMeT4LxsVUar9Aak5pgmUKLqKHLY8NeQxWtKMbQ== alvaro@localhost.localdomain"
	config, err := workerSSHConfig(sshKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	yamlConfig, err := k8syaml.JSONToYAML(config)
	if err != nil {
		t.Fatalf("cannot convert to yaml: %v", err)
	}
	testutil.CompareWithFixture(t, yamlConfig)
}
