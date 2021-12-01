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
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type options struct {
	serviceAccountNamespace   string
	serviceAccountName        string
	tokenAudience             string
	tokenFile                 string
	kubeconfigPath            string
	kubeconfigSecretName      string
	kubeconfigSecretNamespace string
	oneshot                   bool
}

func main() {
	var opts options

	flag.StringVar(&opts.serviceAccountNamespace, "service-account-namespace", "kube-system", "namespace of the service account for which to mint a token")
	flag.StringVar(&opts.serviceAccountName, "service-account-name", "", "name of the service account for which to mint a token")
	flag.StringVar(&opts.tokenAudience, "token-audience", "openshift", "audience for the token")
	flag.StringVar(&opts.tokenFile, "token-file", "/var/run/secrets/openshift/serviceaccount/token", "path to the file where the token will be written")
	flag.StringVar(&opts.kubeconfigPath, "kubeconfig", "/etc/kubernetes/kubeconfig", "path to the kubeconfig file")
	flag.StringVar(&opts.kubeconfigSecretName, "kubeconfig-secret-name", "", "name of a secret containing a kubeconfig key")
	flag.StringVar(&opts.kubeconfigSecretNamespace, "kubeconfig-secret-namespace", "", "namespace of a secret containing a kubeconfig key")
	flag.BoolVar(&opts.oneshot, "oneshot", false, "Exit after minting the token")
	flag.Parse()

	if opts.serviceAccountNamespace == "" ||
		opts.serviceAccountName == "" ||
		opts.tokenAudience == "" ||
		opts.tokenFile == "" ||
		opts.kubeconfigPath == "" {
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

	expirationTimestamp := mintToken(ctx, opts)

	if opts.oneshot {
		return
	}

	for {
		renewDuration := renewDuration(expirationTimestamp)
		fmt.Println("renew delay set for", renewDuration.String())
		select {
		case <-time.After(renewDuration):
			expirationTimestamp = mintToken(ctx, opts)
		case <-ctx.Done():
			return
		}
	}
}

func renewDuration(expirationTimestamp metav1.Time) time.Duration {
	// kubelet waits until 80% of valid time has passed to renew
	// https://github.com/kubernetes/kubernetes/blob/047a6b9f861b2cc9dd2eea77da752ac398e7546f/pkg/kubelet/token/token_manager.go#L186
	return time.Duration(time.Until(expirationTimestamp.Time).Nanoseconds() * 80 / 100)
}

func mintToken(ctx context.Context, opts options) metav1.Time {
	var restConfig *rest.Config
	if opts.kubeconfigSecretName != "" && opts.kubeconfigSecretNamespace != "" {
		config, err := rest.InClusterConfig()
		if err != nil {
			panic(err)
		}
		kubeClient := kubernetes.NewForConfigOrDie(config)
		if err := wait.PollImmediate(time.Second*5, time.Minute*2, func() (done bool, err error) {
			secret, err := kubeClient.CoreV1().Secrets(opts.kubeconfigSecretNamespace).Get(ctx, opts.kubeconfigSecretName, metav1.GetOptions{})
			if err != nil {
				fmt.Println("Unable to get kubeconfig secret, retrying in 5s:", err)
				return false, nil
			}
			kubeconfigBytes, ok := secret.Data["kubeconfig"]
			if !ok {
				return false, fmt.Errorf("kubeconfig secret does not have a kubeconfig key")
			}
			restConfig, err = clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
			if err != nil {
				return false, fmt.Errorf("invalid kubeconfig: %w", err)
			}
			return true, nil
		}); err != nil {
			panic(err)
		}
	} else {
		loadingRules := clientcmd.ClientConfigLoadingRules{ExplicitPath: opts.kubeconfigPath}
		clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(&loadingRules, &clientcmd.ConfigOverrides{})
		var err error
		restConfig, err = clientConfig.ClientConfig()
		if err != nil {
			panic(err)
		}
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		panic(err)
	}

	// Get the service account
	var serviceAccount *corev1.ServiceAccount
	err = wait.PollImmediate(time.Second*5, time.Minute*2, func() (done bool, err error) {
		serviceAccount, err = clientset.CoreV1().ServiceAccounts(opts.serviceAccountNamespace).Get(ctx, opts.serviceAccountName, metav1.GetOptions{})
		if err == nil {
			return true, nil
		}
		if apierrors.IsNotFound(err) {
			return true, err
		}
		fmt.Println("Unable to get service account, retrying in 5s:", err)
		return false, nil
	})

	if !apierrors.IsNotFound(err) {
		fmt.Println("Found existing service account", serviceAccount.GetName())
	} else {
		serviceAccount = &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name: opts.serviceAccountName,
			},
		}
		// Create the service account
		err = wait.PollImmediate(time.Second*5, time.Minute*2, func() (done bool, err error) {
			if serviceAccount, err = clientset.CoreV1().ServiceAccounts(opts.serviceAccountNamespace).Create(ctx, serviceAccount, metav1.CreateOptions{}); err != nil {
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

	treq := &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			Audiences: []string{opts.tokenAudience},
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
	f, err := os.Create(opts.tokenFile)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	f.WriteString(token.Status.Token)

	fmt.Println("Successfully wrote token to", opts.tokenFile)
	return token.Status.ExpirationTimestamp
}
