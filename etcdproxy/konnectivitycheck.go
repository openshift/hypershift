package etcdproxy

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/util/wait"
)

type KonnectivityChecker struct {
	metricsURL string
	ready      chan struct{}
	logger     *zap.Logger
}

func NewKonnectivityChecker(lg *zap.Logger, adminURL string, readyChan chan struct{}) *KonnectivityChecker {
	return &KonnectivityChecker{
		metricsURL: adminURL,
		ready:      readyChan,
		logger:     lg,
	}
}

func (k *KonnectivityChecker) Run(c context.Context) error {
	// First, wait until an agent has been registered.
	err := wait.PollUntilContextCancel(c, 15*time.Second, true, func(ctx context.Context) (bool, error) {
		select {
		case <-ctx.Done():
			return false, nil
		default:
		}
		resp, err := http.Get(k.metricsURL)
		if err != nil {
			k.logger.Error("failed to get metrics", zap.Error(err))
			return false, nil
		}
		agentCount, err := getAgentCount(resp.Body)
		if err != nil {
			k.logger.Error("failed to get agent count", zap.Error(err))
			return false, nil
		}
		if agentCount == 0 {
			return false, nil
		}
		k.logger.Info("konnectivity agent registered", zap.Int("count", agentCount))
		return true, nil
	})
	if err != nil {
		k.logger.Error("failed to wait for konnectivity agent", zap.Error(err))
		return err
	}

	// After the first agent has been registered, wait a fixed amount of time to allow more agents to register.
	k.logger.Info("Waiting for more agents to register")
	time.Sleep(90 * time.Second)
	k.ready <- struct{}{}
	return nil
}

func getAgentCount(metricsBody io.Reader) (int, error) {
	scanner := bufio.NewScanner(metricsBody)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "konnectivity_network_proxy_server_ready_backend_connections ") {
			parts := strings.Split(line, " ")
			if len(parts) == 2 {
				value, err := strconv.Atoi(parts[1])
				if err != nil {
					return 0, err
				}
				return value, nil
			}
		}
	}
	return 0, scanner.Err()
}
