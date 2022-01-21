package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

const ErrorRetryPeriod = 10 * time.Second

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

	if opts.oneshot {
		_, err := mintToken(ctx, opts)
		if err != nil {
			log.Fatalln(err)
		}
		os.Exit(0)
	}

	var renewDuration time.Duration
	for {
		select {
		case <-time.After(renewDuration):
			log.Println("minting token")
			expirationTimestamp, err := mintToken(ctx, opts)
			if err != nil {
				log.Println("error minting token, will retry in", ErrorRetryPeriod.String(), err)
				renewDuration = ErrorRetryPeriod
			} else {
				renewDuration = renewDurationFromExpiration(expirationTimestamp)
				log.Println("renew delay set for", renewDuration.String())
			}
		case <-ctx.Done():
			return
		}
	}
}

func renewDurationFromExpiration(expirationTimestamp metav1.Time) time.Duration {
	// kubelet waits until 80% of valid time has passed to renew
	// https://github.com/kubernetes/kubernetes/blob/047a6b9f861b2cc9dd2eea77da752ac398e7546f/pkg/kubelet/token/token_manager.go#L186
	return time.Duration(time.Until(expirationTimestamp.Time).Nanoseconds() * 80 / 100)
}

func mintToken(ctx context.Context, opts options) (metav1.Time, error) {
	var restConfig *rest.Config
	if opts.kubeconfigSecretName != "" && opts.kubeconfigSecretNamespace != "" {
		config, err := rest.InClusterConfig()
		if err != nil {
			return metav1.Time{}, fmt.Errorf("failed to get kubeconfig: %w", err)
		}
		kubeClient, err := kubernetes.NewForConfig(config)
		if err != nil {
			return metav1.Time{}, fmt.Errorf("failed to make kube client: %w", err)
		}
		apiContext, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		secret, err := kubeClient.CoreV1().Secrets(opts.kubeconfigSecretNamespace).Get(apiContext, opts.kubeconfigSecretName, metav1.GetOptions{})
		if err != nil {
			return metav1.Time{}, fmt.Errorf("failed to get kubeconfig secret: %w", err)
		}
		kubeconfigBytes, ok := secret.Data["kubeconfig"]
		if !ok {
			return metav1.Time{}, fmt.Errorf("kubeconfig secret does not have a kubeconfig key")
		}
		restConfig, err = clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
		if err != nil {
			return metav1.Time{}, fmt.Errorf("invalid kubeconfig: %w", err)
		}
	} else {
		loadingRules := clientcmd.ClientConfigLoadingRules{ExplicitPath: opts.kubeconfigPath}
		clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(&loadingRules, &clientcmd.ConfigOverrides{})
		var err error
		restConfig, err = clientConfig.ClientConfig()
		if err != nil {
			return metav1.Time{}, fmt.Errorf("failed to get client config: %w", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return metav1.Time{}, fmt.Errorf("failed to get guest kube client: %w", err)
	}

	// Get the service account
	apiContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	serviceAccountExists := false
	serviceAccount, err := clientset.CoreV1().ServiceAccounts(opts.serviceAccountNamespace).Get(apiContext, opts.serviceAccountName, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return metav1.Time{}, fmt.Errorf("failed to get serviceaccount: %w", err)
		}
	} else {
		serviceAccountExists = true
	}

	if serviceAccountExists {
		log.Println("Found existing service account", serviceAccount.GetName())
	} else {
		serviceAccount = &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name: opts.serviceAccountName,
			},
		}
		// Create the service account
		apiContext, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		serviceAccount, err = clientset.CoreV1().ServiceAccounts(opts.serviceAccountNamespace).Create(apiContext, serviceAccount, metav1.CreateOptions{})
		if err != nil {
			if apierrors.IsAlreadyExists(err) {
				log.Println("Service account already exists", serviceAccount.GetName())
			} else {
				return metav1.Time{}, fmt.Errorf("failed to create serviceaccount: %w", err)
			}
		} else {
			log.Println("Created service account", serviceAccount.GetName())
		}
	}

	treq := &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			Audiences: []string{opts.tokenAudience},
		},
	}

	// Create the service account token
	apiContext, cancel = context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	token, err := clientset.CoreV1().ServiceAccounts(serviceAccount.GetNamespace()).CreateToken(apiContext, serviceAccount.GetName(), treq, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			log.Println("Token already exists", token.GetName())
		} else {
			return metav1.Time{}, fmt.Errorf("failed to create token: %w", err)
		}
	} else {
		log.Println("Created service account token for service account", serviceAccount.GetName())
	}

	// Write token to file
	f, err := os.Create(opts.tokenFile)
	if err != nil {
		return metav1.Time{}, fmt.Errorf("failed to create token file: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(token.Status.Token); err != nil {
		return metav1.Time{}, fmt.Errorf("failed to write token file: %w", err)
	}

	log.Println("Successfully wrote token to", opts.tokenFile)
	return token.Status.ExpirationTimestamp, nil
}
