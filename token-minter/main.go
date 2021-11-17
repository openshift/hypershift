package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	var serviceAccountNamespace string
	var serviceAccountName string
	var tokenAudience string
	var tokenFile string
	var kubeconfigPath string
	var sleep bool

	flag.StringVar(&serviceAccountNamespace, "service-account-namespace", "kube-system", "namespace of the service account for which to mint a token")
	flag.StringVar(&serviceAccountName, "service-account-name", "", "name of the service account for which to mint a token")
	flag.StringVar(&tokenAudience, "token-audience", "openshift", "audience for the token")
	flag.StringVar(&tokenFile, "token-file", "/var/run/secrets/openshift/serviceaccount/token", "path to the file where the token will be written")
	flag.StringVar(&kubeconfigPath, "kubeconfig", "/etc/kubernetes/kubeconfig", "path to the kubeconfig file")
	flag.BoolVar(&sleep, "sleep", false, "If the binary should sleep after finishing. Required when running as a sidecar, as otherwise the container will be considered crashing.")
	flag.Parse()

	if serviceAccountNamespace == "" ||
		serviceAccountName == "" ||
		tokenAudience == "" ||
		tokenFile == "" ||
		kubeconfigPath == "" {
		flag.Usage()
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())

	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		cancel()
		<-c
		os.Exit(1) // second signal. Exit directly.
	}()

	loadingRules := clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath}
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(&loadingRules, &clientcmd.ConfigOverrides{})
	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		panic(err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		panic(err)
	}

	// Get the service account
	var serviceAccount *corev1.ServiceAccount
	err = wait.PollImmediate(time.Second*5, time.Minute*2, func() (done bool, err error) {
		serviceAccount, err = clientset.CoreV1().ServiceAccounts(serviceAccountNamespace).Get(ctx, serviceAccountName, metav1.GetOptions{})
		if err == nil {
			return true, nil
		}
		if apierrors.IsNotFound(err) {
			return true, err
		}
		fmt.Println("Unable to get service account, retry in 10s:", err)
		return false, nil
	})

	if !apierrors.IsNotFound(err) {
		fmt.Println("Found existing service account", serviceAccount.GetName())
	} else {
		serviceAccount = &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name: serviceAccountName,
			},
		}
		// Create the service account
		err = wait.PollImmediate(time.Second*5, time.Minute*2, func() (done bool, err error) {
			if serviceAccount, err = clientset.CoreV1().ServiceAccounts(serviceAccountNamespace).Create(ctx, serviceAccount, metav1.CreateOptions{}); err != nil {
				fmt.Println("Unable to create service account, retry in 10s:", err)
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			fmt.Println("Unable to create service account:", err)
			panic(err)
		}
		fmt.Println("Created service account", serviceAccount.GetName())
	}

	expSeconds := int64(60 * 60 * 24 * 365) // 1 year
	treq := &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			Audiences:         []string{tokenAudience},
			ExpirationSeconds: &expSeconds,
		},
	}

	// Create the service account token
	var token *authenticationv1.TokenRequest
	err = wait.PollImmediate(time.Second*5, time.Minute*2, func() (done bool, err error) {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if token, err = clientset.CoreV1().ServiceAccounts(serviceAccount.GetNamespace()).CreateToken(ctx, serviceAccount.GetName(), treq, metav1.CreateOptions{}); err != nil {
			fmt.Println("Unable to create service account token, retry in 10s:", err)
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		fmt.Printf("Unable to create service account token: %v", err)
		panic(err)
	}

	fmt.Println("Created service account token for service account", serviceAccount.GetName())

	// Write token to file
	f, err := os.Create(tokenFile)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	f.WriteString(token.Status.Token)

	fmt.Println("Successfully wrote token to", tokenFile)
	if sleep {
		fmt.Println("Done, starting to sleep")
		<-ctx.Done()
	}
}
