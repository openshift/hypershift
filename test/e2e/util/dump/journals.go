package dump

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	bastionaws "github.com/openshift/hypershift/cmd/bastion/aws"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	cmdutil "github.com/openshift/hypershift/cmd/util"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/ec2"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

//go:embed copy-machine-journals.sh
var copyJournalsScript []byte

func DumpJournals(t *testing.T, ctx context.Context, hc *hyperv1.HostedCluster, artifactDir, awsCreds string) error {
	// Write out private ssh key
	secretName := hc.Spec.SSHKey.Name
	if len(secretName) == 0 {
		return fmt.Errorf("no SSH secret specified for cluster, cannot dump journals")
	}

	sshKeySecret := &corev1.Secret{}
	sshKeySecret.Name = secretName
	sshKeySecret.Namespace = hc.Namespace
	kubeClient, err := cmdutil.GetClient()
	if err != nil {
		return err
	}
	if err := kubeClient.Get(ctx, client.ObjectKeyFromObject(sshKeySecret), sshKeySecret); err != nil {
		return err
	}
	privateKey, exists := sshKeySecret.Data["id_rsa"]
	if !exists {
		return fmt.Errorf("cannot find SSH private key in SSH key secret %s/%s", sshKeySecret.Namespace, sshKeySecret.Name)
	}
	privateSSHKeyDir, err := os.MkdirTemp("", "")
	if err != nil {
		return fmt.Errorf("cannot create temp dir for ssh key: %w", err)
	}
	privateKeyFile := filepath.Join(privateSSHKeyDir, "id_rsa")
	if err := os.WriteFile(privateKeyFile, privateKey, 0600); err != nil {
		return fmt.Errorf("error writing private ssh key file: %w", err)
	}

	// Write out dump script where we can find it and invoke it
	copyJournalFile, err := os.CreateTemp("", "copy-journal-")
	if err != nil {
		return err
	}
	if err := copyJournalFile.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(copyJournalFile.Name(), copyJournalsScript, 0644); err != nil {
		return err
	}
	if err := os.Chmod(copyJournalFile.Name(), 0755); err != nil {
		return err
	}

	createLogFile := filepath.Join(artifactDir, "create-bastion.log")
	createLog, err := os.Create(createLogFile)
	if err != nil {
		return fmt.Errorf("failed to create create log: %w", err)
	}
	createLogger := zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.Lock(createLog), zap.DebugLevel))
	defer func() {
		if err := createLogger.Sync(); err != nil {
			fmt.Printf("failed to sync createLogger: %v\n", err)
		}
	}()

	destroyLogFile := filepath.Join(artifactDir, "destroy-bastion.log")
	destroyLog, err := os.Create(destroyLogFile)
	if err != nil {
		return fmt.Errorf("failed to create destroy log: %w", err)
	}
	destroyLogger := zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.Lock(destroyLog), zap.DebugLevel))
	defer func() {
		if err := destroyLogger.Sync(); err != nil {
			fmt.Printf("failed to sync destroyLogger: %v\n", err)
		}
	}()

	var bastionIP string
	// Only create a bastion if the cluster is not using public IPs
	if hc.Annotations[hyperv1.AWSMachinePublicIPs] != "true" {
		// Create a bastion
		createBastion := bastionaws.CreateBastionOpts{
			Namespace:          hc.Namespace,
			Name:               hc.Name,
			AWSCredentialsFile: awsCreds,
			Wait:               true,
		}
		_, bastionIP, err = createBastion.Run(ctx, zapr.NewLoggerWithOptions(createLogger))
		if err != nil {
			return err
		}
		defer func() {
			destroyBastion := bastionaws.DestroyBastionOpts{
				Namespace:          hc.Namespace,
				Name:               hc.Name,
				AWSCredentialsFile: awsCreds,
			}
			if err := destroyBastion.Run(ctx, zapr.NewLoggerWithOptions(destroyLogger)); err != nil {
				t.Logf("error destroying bastion: %v", err)
			}
		}()
	}

	// Find worker machine IPs
	awsSession := awsutil.NewSession("cli-destroy-bastion", awsCreds, "", "", hc.Spec.Platform.AWS.Region)
	awsConfig := awsutil.NewConfig()
	ec2Client := ec2.New(awsSession, awsConfig)

	result, err := ec2Client.DescribeInstancesWithContext(ctx, &ec2.DescribeInstancesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("tag:kubernetes.io/cluster/" + hc.Spec.InfraID),
				Values: []*string{aws.String("owned")},
			},
		},
	})
	if err != nil {
		return err
	}
	var machineIPs []string
	var machineInstances []*ec2.Instance
	var sgID string
	for _, reservation := range result.Reservations {
		for _, instance := range reservation.Instances {
			skip := false
			for _, tag := range instance.Tags {
				if aws.StringValue(tag.Key) == "Name" && aws.StringValue(tag.Value) == (hc.Spec.InfraID+"-bastion") {
					skip = true
					break
				}
			}
			if skip {
				continue
			}

			if *instance.State.Name == "running" {
				if hc.Annotations[hyperv1.AWSMachinePublicIPs] == "true" {
					if aws.StringValue(instance.PublicIpAddress) != "" {
						if sgID == "" && len(instance.SecurityGroups) > 0 {
							sgID = aws.StringValue(instance.SecurityGroups[0].GroupId)
						}
						machineIPs = append(machineIPs, aws.StringValue(instance.PublicIpAddress))
					}
				} else {
					machineIPs = append(machineIPs, aws.StringValue(instance.PrivateIpAddress))
				}
				machineInstances = append(machineInstances, instance)
			}
		}
	}

	// Add an inbound rule to allow SSH access (idempotent, optionally scoped)
	if sgID != "" {
		sourceCIDR := os.Getenv("SSH_SOURCE_CIDR")
		if sourceCIDR == "" {
			sourceCIDR = "0.0.0.0/0"
		}
		permissions := []*ec2.IpPermission{
			{
				IpProtocol: aws.String("tcp"),
				IpRanges: []*ec2.IpRange{
					{
						CidrIp: aws.String(sourceCIDR),
					},
				},
				FromPort: aws.Int64(22),
				ToPort:   aws.Int64(22),
			},
		}
		_, err = ec2Client.AuthorizeSecurityGroupIngressWithContext(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
			GroupId:       aws.String(sgID),
			IpPermissions: permissions,
		})
		if err != nil {
			// Tolerate duplicate rules to make this idempotent
			if aerr, ok := err.(awserr.Error); !ok || aerr.Code() != "InvalidPermission.Duplicate" {
				return fmt.Errorf("failed to authorize security group: %w", err)
			}
		}
	}

	if len(machineIPs) == 0 {
		t.Logf("No machines associated with infra id %s were found. Skipping journal dump.", hc.Spec.InfraID)
		return nil
	}

	// Invoke script
	dumpJournalsLogFile := filepath.Join(artifactDir, "dump-machine-journals.log")
	dumpJournalsLog, err := os.Create(dumpJournalsLogFile)
	if err != nil {
		return fmt.Errorf("failed to create dumpJournals log: %w", err)
	}

	outputDir := filepath.Join(artifactDir, "machine-journals")
	scriptCmd := exec.Command(copyJournalFile.Name(), outputDir)
	env := os.Environ()
	env = append(env, fmt.Sprintf("BASTION_IP=%s", bastionIP))
	env = append(env, fmt.Sprintf("INSTANCE_IPS=%s", strings.Join(machineIPs, " ")))
	env = append(env, fmt.Sprintf("SSH_PRIVATE_KEY=%s", privateKeyFile))
	if hc.Annotations[hyperv1.AWSMachinePublicIPs] == "true" {
		env = append(env, "AWS_MACHINE_PUBLIC_IPS=true")
	}
	scriptCmd.Env = env
	scriptCmd.Stdout = dumpJournalsLog
	scriptCmd.Stderr = dumpJournalsLog
	err = scriptCmd.Run()
	if err != nil {
		var errorList []string
		t.Logf("Error copying machine journals to artifacts directory: %v", err)
		for _, instance := range machineInstances {
			err = os.WriteFile(filepath.Join(outputDir, fmt.Sprintf("instance-%s.txt", aws.StringValue(instance.InstanceId))), []byte(instance.String()), 0644)
			if err != nil {
				errorList = append(errorList, fmt.Sprintf("failed to write file to artifacts directory: %v. ", err))
			}
		}
		if len(errorList) > 0 {
			return fmt.Errorf("error writing machine journals to artifacts: %v", errorList)
		}
	} else {
		t.Logf("Successfully copied machine journals to %s", outputDir)
	}
	return err
}
