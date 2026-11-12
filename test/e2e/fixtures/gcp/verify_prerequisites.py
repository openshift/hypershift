#!/usr/bin/env python3
"""Verify the vendored GKE prerequisites without accessing upstream sources."""

import hashlib
import json
from pathlib import Path
import unittest

import yaml


ROOT = Path(__file__).resolve().parent / "prerequisites"


class PrerequisitesTest(unittest.TestCase):
    def test_checksums(self):
        sources = json.loads((ROOT / "sources.json").read_text())
        self.assertEqual(
            set(sources),
            {path.name for path in ROOT.iterdir()} - {"README.md", "sources.json"},
        )
        for filename, source in sources.items():
            with self.subTest(filename=filename):
                self.assertTrue(source["url"].startswith("https://"))
                self.assertNotIn("/main/", source["url"])
                self.assertNotIn("/master/", source["url"])
                self.assertTrue(source["revision"])
                self.assertEqual(
                    hashlib.sha256((ROOT / filename).read_bytes()).hexdigest(),
                    source["sha256"],
                )

    def test_crd_identity(self):
        expected = {
            "monitoring.coreos.com_servicemonitors.yaml": "servicemonitors.monitoring.coreos.com",
            "monitoring.coreos.com_prometheusrules.yaml": "prometheusrules.monitoring.coreos.com",
            "monitoring.coreos.com_podmonitors.yaml": "podmonitors.monitoring.coreos.com",
            "routes.crd.yaml": "routes.route.openshift.io",
        }
        for filename, name in expected.items():
            with self.subTest(filename=filename):
                manifest = yaml.safe_load((ROOT / filename).read_text())
                self.assertEqual(manifest["kind"], "CustomResourceDefinition")
                self.assertEqual(manifest["apiVersion"], "apiextensions.k8s.io/v1")
                self.assertEqual(manifest["metadata"]["name"], name)

    def test_cert_manager_bundle(self):
        manifests = list(yaml.safe_load_all((ROOT / "cert-manager.yaml").read_text()))
        deployments = {obj["metadata"]["name"]: obj for obj in manifests if obj and obj["kind"] == "Deployment"}
        self.assertEqual(set(deployments), {"cert-manager", "cert-manager-webhook", "cert-manager-cainjector"})
        for deployment in deployments.values():
            self.assertEqual(deployment["metadata"]["namespace"], "cert-manager")
            for container in deployment["spec"]["template"]["spec"]["containers"]:
                self.assertTrue(container["image"].endswith(":v1.14.0"))
        crds = {obj["metadata"]["name"] for obj in manifests if obj and obj["kind"] == "CustomResourceDefinition"}
        self.assertIn("clusterissuers.cert-manager.io", crds)
        self.assertIn("certificates.cert-manager.io", crds)


if __name__ == "__main__":
    unittest.main()
