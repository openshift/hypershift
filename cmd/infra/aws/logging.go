package aws

import "sigs.k8s.io/controller-runtime/pkg/log/zap"

var log = zap.New(zap.UseDevMode(true), zap.JSONEncoder())
