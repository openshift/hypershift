package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"

	admissionv1 "k8s.io/api/admission/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/google/go-cmp/cmp"
)

func main() {
	ctx := ctrl.SetupSignalHandler()

	restConfig := ctrl.GetConfigOrDie()
	restConfig.UserAgent = "admission-differ"
	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme: hyperapi.Scheme,
		WebhookServer: webhook.NewServer(webhook.Options{
			Port:    9443,
			CertDir: "/var/run/secrets/serving-cert",
		}),
	})
	if err != nil {
		log.Fatalf("unable to start manager: %s", err.Error())
	}

	hookServer := mgr.GetWebhookServer()
	hookServer.Register("/awsendpointservices", &webhook.Admission{Handler: &awsEndpointServiceAdmissionTracer{decoder: admission.NewDecoder(mgr.GetScheme())}})

	err = mgr.Start(ctx)
	if err != nil {
		log.Fatalf("Start returned with error: %s", err.Error())
	}
}

type awsEndpointServiceAdmissionTracer struct {
	decoder admission.Decoder
}

var _ admission.Handler = &awsEndpointServiceAdmissionTracer{}

func (v *awsEndpointServiceAdmissionTracer) Handle(_ context.Context, req admission.Request) admission.Response {
	new := &hyperv1.AWSEndpointService{}
	err := v.decoder.Decode(req, new)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	var output bytes.Buffer
	fmt.Fprintf(&output, "%s %s %s\n", new.Name, req.Operation, req.UserInfo.Username)
	switch req.Operation {
	case admissionv1.Create:
		fmt.Fprintf(&output, "+%v", new)
	case admissionv1.Update:
		old := &hyperv1.AWSEndpointService{}
		err = v.decoder.DecodeRaw(req.OldObject, old)
		if err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		fmt.Fprint(&output, cmp.Diff(old, new))
	}
	fmt.Println(output.String())
	return admission.Allowed("")
}
