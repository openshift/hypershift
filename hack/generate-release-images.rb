#!/usr/bin/env ruby
require 'yaml'
require 'json'
require 'net/http'
require 'base64'

major_versions = ["4.6"]

images = {}
major_versions.each do |major|
  data = Net::HTTP.get("mirror.openshift.com", "/pub/openshift-v4/clients/ocp/candidate-#{major}/release.txt")
  image = data.match(/Pull From.*$/)[0].split[2]
  images[image] = JSON.load(`oc adm release info --output json #{image}`)
end

configmap = {
  "apiVersion" => "v1",
  "kind" => "ConfigMap",
  "metadata" => {
    "namespace" => "hypershift",
    "name" => "release-images",
  },
  "data" => {
    "images.json" => JSON.dump(images)
  }
}

puts YAML.dump(configmap)
