//go:build e2ev2

package main

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	bastionaws "github.com/openshift/hypershift/cmd/bastion/aws"
	consoleaws "github.com/openshift/hypershift/cmd/consolelogs/aws"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	cmdutil "github.com/openshift/hypershift/cmd/util"
	supportawsutil "github.com/openshift/hypershift/support/awsutil"
	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"

	corev1 "k8s.io/api/core/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type journalEC2Client interface {
	DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	AuthorizeSecurityGroupIngress(context.Context, *ec2.AuthorizeSecurityGroupIngressInput, ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupIngressOutput, error)
}

type journalOperations struct {
	createBastion          func(context.Context, logr.Logger) (string, error)
	destroyBastion         func(context.Context, logr.Logger) error
	copyJournal            func(context.Context, string, string, string, io.Writer, io.Writer) error
	removePrivateDirectory func(string) error
}

func (operations journalOperations) removeKeyDirectory(directory string) error {
	remove := operations.removePrivateDirectory
	if remove == nil {
		remove = os.RemoveAll
	}
	if err := remove(directory); err != nil {
		var pathError *os.PathError
		if errors.As(err, &pathError) {
			err = pathError.Err
		}
		return fmt.Errorf("remove temporary journal key directory: %w", err)
	}
	return nil
}

func dumpAWSConsole(parent context.Context, entry lifecycle.ClusterEntry, directory string) error {
	ctx, cancel := context.WithTimeout(parent, teardownDiagnosticTimeout)
	defer cancel()
	client, err := cmdutil.GetClientWithKubeconfig("")
	if err != nil {
		return err
	}
	options := &consoleaws.ConsoleLogOpts{Name: entry.Name, Namespace: entry.Namespace, OutputDir: filepath.Join(directory, "machine-console-logs")}
	options.AWSCredentialsOpts.AWSCredentialsFile = teardownAWSCredentialsFile()
	return options.Run(ctx, client)
}

func dumpAWSJournals(parent context.Context, entry lifecycle.ClusterEntry, directory string) error {
	ctx, cancel := context.WithTimeout(parent, teardownDiagnosticTimeout)
	defer cancel()
	client, err := cmdutil.GetClientWithKubeconfig("")
	if err != nil {
		return err
	}
	cluster := &hyperv1.HostedCluster{}
	if err := client.Get(ctx, crclient.ObjectKey{Namespace: entry.Namespace, Name: entry.Name}, cluster); err != nil {
		return err
	}
	if cluster.Spec.Platform.AWS == nil {
		return fmt.Errorf("AWS platform is missing for %s/%s", entry.Namespace, entry.Name)
	}
	credentials := teardownAWSCredentialsFile()
	session, err := (&awsutil.AWSCredentialsOptions{AWSCredentialsFile: credentials}).GetSession(ctx, "e2ev2-journals", nil, cluster.Spec.Platform.AWS.Region)
	if err != nil {
		return err
	}
	tags := []string{supportawsutil.HypershiftSourceTagKey + "=e2e"}
	if jobID := os.Getenv("PROW_JOB_ID"); jobID != "" {
		tags = append(tags, supportawsutil.HypershiftProwJobIDTagKey+"="+jobID)
	}
	create := &bastionaws.CreateBastionOpts{Name: cluster.Name, Namespace: cluster.Namespace, AWSCredentialsFile: credentials, Wait: true, AdditionalTags: tags}
	destroy := &bastionaws.DestroyBastionOpts{InfraID: cluster.Spec.InfraID, Region: cluster.Spec.Platform.AWS.Region, AWSCredentialsFile: credentials}
	return collectAWSJournals(ctx, cluster, client, ec2.NewFromConfig(*session), directory, journalOperations{
		createBastion: func(ctx context.Context, logger logr.Logger) (string, error) {
			_, address, err := create.Run(ctx, logger, client)
			return address, err
		},
		destroyBastion: func(ctx context.Context, logger logr.Logger) error { return destroy.Run(ctx, logger, client) },
		copyJournal:    copyAWSJournal,
	})
}

func teardownAWSCredentialsFile() string {
	if credentials := os.Getenv("AWS_GUEST_INFRA_CREDENTIALS_FILE"); credentials != "" {
		return credentials
	}
	return os.Getenv("AWS_SHARED_CREDENTIALS_FILE")
}

func collectAWSJournals(ctx context.Context, cluster *hyperv1.HostedCluster, client crclient.Client, cloud journalEC2Client, directory string, operations journalOperations) (result error) {
	if cluster.Spec.SSHKey.Name == "" {
		return fmt.Errorf("no SSH secret specified for cluster, cannot dump journals")
	}
	secret := &corev1.Secret{}
	if err := client.Get(ctx, crclient.ObjectKey{Namespace: cluster.Namespace, Name: cluster.Spec.SSHKey.Name}, secret); err != nil {
		return err
	}
	if len(secret.Data["id_rsa"]) == 0 {
		return fmt.Errorf("cannot find SSH private key in SSH key secret %s/%s", secret.Namespace, secret.Name)
	}
	privateDirectory, err := os.MkdirTemp("", "e2ev2-journal-key-")
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, operations.removeKeyDirectory(privateDirectory))
	}()
	privateKey := filepath.Join(privateDirectory, "id_rsa")
	if err := os.WriteFile(privateKey, secret.Data["id_rsa"], 0600); err != nil {
		return err
	}
	logfile, err := os.Create(filepath.Join(directory, "dump-machine-journals.log"))
	if err != nil {
		return err
	}
	defer func() {
		if err := logfile.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close journal log: %w", err))
		}
	}()
	createLog, err := os.Create(filepath.Join(directory, "create-bastion.log"))
	if err != nil {
		return err
	}
	createLogger := zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.Lock(createLog), zap.DebugLevel))
	defer func() {
		result = errors.Join(result, closeDiagnosticLog(createLog, createLogger))
	}()
	destroyLog, err := os.Create(filepath.Join(directory, "destroy-bastion.log"))
	if err != nil {
		return err
	}
	destroyLogger := zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.Lock(destroyLog), zap.DebugLevel))
	defer func() {
		result = errors.Join(result, closeDiagnosticLog(destroyLog, destroyLogger))
	}()
	bastion := ""
	public := cluster.Annotations[hyperv1.AWSMachinePublicIPs] == "true"
	if !public {
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			result = errors.Join(result, operations.destroyBastion(cleanupCtx, zapr.NewLogger(destroyLogger)))
		}()
		bastion, err = operations.createBastion(ctx, zapr.NewLogger(createLogger))
		if err != nil {
			return err
		}
		if net.ParseIP(bastion) == nil {
			return fmt.Errorf("bastion did not return an IP address")
		}
	}
	instances, err := journalInstances(ctx, cloud, cluster.Spec.InfraID)
	if err != nil {
		return err
	}
	outputDir := filepath.Join(directory, "machine-journals")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return err
	}
	for _, instance := range instances {
		address := aws.ToString(instance.PrivateIpAddress)
		if public {
			address = aws.ToString(instance.PublicIpAddress)
			if address == "" {
				continue
			}
			if err := authorizeJournalSSH(ctx, cloud, instance.SecurityGroups); err != nil {
				result = errors.Join(result, err)
				continue
			}
		}
		if net.ParseIP(address) == nil {
			result = errors.Join(result, fmt.Errorf("instance %s has no valid journal IP address", aws.ToString(instance.InstanceId)))
			continue
		}
		var copyError error
		for attempt := 0; attempt < 10; attempt++ {
			file, err := os.Create(filepath.Join(outputDir, "journal_"+address+".log.gz"))
			if err != nil {
				copyError = err
				break
			}
			compressed := gzip.NewWriter(file)
			copyError = errors.Join(operations.copyJournal(ctx, address, bastion, privateKey, compressed, logfile), compressed.Close(), file.Close())
			if copyError == nil || ctx.Err() != nil {
				break
			}
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
			if ctx.Err() != nil {
				break
			}
		}
		if copyError != nil {
			result = errors.Join(result, fmt.Errorf("copy journal from %s: %w", address, errors.Join(copyError, ctx.Err())))
			if err := os.WriteFile(filepath.Join(outputDir, "instance-"+filepath.Base(aws.ToString(instance.InstanceId))+".txt"), []byte(fmt.Sprintf("%+v", instance)), 0644); err != nil {
				result = errors.Join(result, err)
			}
		}
	}
	return result
}

func journalInstances(ctx context.Context, cloud journalEC2Client, infraID string) ([]ec2types.Instance, error) {
	var instances []ec2types.Instance
	pages := ec2.NewDescribeInstancesPaginator(cloud, &ec2.DescribeInstancesInput{Filters: []ec2types.Filter{{Name: aws.String("tag:kubernetes.io/cluster/" + infraID), Values: []string{"owned"}}}})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, reservation := range page.Reservations {
			for _, instance := range reservation.Instances {
				if instance.State == nil || instance.State.Name != ec2types.InstanceStateNameRunning {
					continue
				}
				bastion := false
				for _, tag := range instance.Tags {
					bastion = bastion || aws.ToString(tag.Key) == "Name" && aws.ToString(tag.Value) == infraID+"-bastion"
				}
				if !bastion {
					instances = append(instances, instance)
				}
			}
		}
	}
	return instances, nil
}

func authorizeJournalSSH(ctx context.Context, cloud journalEC2Client, groups []ec2types.GroupIdentifier) error {
	if len(groups) == 0 || aws.ToString(groups[0].GroupId) == "" {
		return fmt.Errorf("worker has no security group for public journal access")
	}
	cidr := os.Getenv("SSH_SOURCE_CIDR")
	if cidr == "" {
		cidr = "0.0.0.0/0"
	}
	_, err := cloud.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{GroupId: groups[0].GroupId, IpPermissions: []ec2types.IpPermission{{IpProtocol: aws.String("tcp"), FromPort: aws.Int32(22), ToPort: aws.Int32(22), IpRanges: []ec2types.IpRange{{CidrIp: aws.String(cidr)}}}}})
	var apiError smithy.APIError
	if errors.As(err, &apiError) && apiError.ErrorCode() == "InvalidPermission.Duplicate" {
		return nil
	}
	return err
}

func copyAWSJournal(ctx context.Context, address, bastion, key string, stdout, stderr io.Writer) error {
	arguments := []string{"-i", key, "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10", "-o", "LogLevel=ERROR"}
	if bastion != "" {
		arguments = append(arguments, "-o", fmt.Sprintf("ProxyCommand=ssh -i %q -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=10 -o LogLevel=ERROR -W %%h:%%p ec2-user@%s", key, bastion))
	}
	arguments = append(arguments, "core@"+address, "sudo journalctl --no-pager")
	command := exec.CommandContext(ctx, "ssh", arguments...)
	command.Stdout, command.Stderr = stdout, stderr
	return command.Run()
}
