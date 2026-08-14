# General
This directory contains scripts to help with snyk duty. The scripts are written in bash.
1. `check-for-cve-in-ho.sh` - This script returns versions of packages for the specified package in the specified HO image. Uses Podman.
2. `check-for-cve-in-cpo.sh` - This script returns versions of packages for the specified package in the specified CPO image. Uses Podman.
3. `check-for-cve.sh` - This script returns versions of the specified package for the latest HO image and the latest nightly CPO image for 4.14-4.18. Uses Docker.

## Podman
This script was created to quickly look for relevant CVEs in either the HO or CPO in a release image during snyk duty. To run this locally:

1. Update the PULL_SECRETS_FILE env var as necessary in `check-for-cve-in-ho.sh` and `check-for-cve-in-cpo.sh`
2. Run this commands below in your terminal passing in the search strings like `'wget,cpython,gnutls,glib'` with no spaces
   1. For the example below, I was looking for `wget or cpython or gnutls or glib` in the CPO image of a 4.16 release image

### HO Example
For the example below, I was looking for `wget or cpython or gnutls or glib` in the HO image

```
bash-5.1$ . ./hypershift/contrib/snyk/check-for-cve-in-ho.sh quay.io/acm-d/rhtap-hypershift-operator:183b61b wget,cpython,gnutls,glib
Searching HO image
7ce5a41378fa07255b29a99922b2247e5fcef9713cadbce41b565a218bc83992
glibc-common-2.34-100.el9_4.2.aarch64
glibc-minimal-langpack-2.34-100.el9_4.2.aarch64
glibc-2.34-100.el9_4.2.aarch64
gnutls-3.8.3-4.el9_4.aarch64
glib2-2.68.4-14.el9.aarch64
json-glib-1.6.6-1.el9.aarch64
```

### CPO Example
For the example below, I was looking for `wget or cpython or gnutls or glib` in the CPO image of a 4.16 release image

```
bash-5.1$ . ./hypershift/contrib/snyk/check-for-cve-in-cpo.sh quay.io/openshift-release-dev/ocp-release:4.16.3-multi wget,cpython,gnutls,glib
Searching CPO image
1b207e9654ffbfe6526f88064be0d42e9894dc376d03863c33817497b258c4cc
glib2-2.68.4-6.el9.aarch64
json-glib-1.6.6-1.el9.aarch64
glibc-common-2.34-60.el9_2.14.aarch64
glibc-minimal-langpack-2.34-60.el9_2.14.aarch64
glibc-2.34-60.el9_2.14.aarch64
glibc-langpack-en-2.34-60.el9_2.14.aarch64
gnutls-3.7.6-21.el9_2.3.aarch64
wget-1.21.1-7.el9.aarch64
```

## Docker

### HO + CPO example
To run this locally you will need to have Docker installed and running. You will also need to have the `PULL_SECRET` env var set to the contents of your pull secret file. Example of a search for the `krb5` package is shown below:
```
PULL_SECRET=$PULL_SECRET /bin/bash /Users/pstefans/GitHub/openshift/hypershift/contrib/snyk/check-for-cve.sh krb5

===== Searching HO Image =====
quay.io/acm-d/rhtap-hypershift-operator:latest
Image: quay.io/acm-d/rhtap-hypershift-operator:latest
krb5-libs-1.21.1-1.el9.aarch64
=============================================

===== Searching CPO Image Version 4.14 =====
Image: quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:31ec47fa526b27e9073468c273bee662fabc11c576e2b16955f96d1ca3df22c2
quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:31ec47fa526b27e9073468c273bee662fabc11c576e2b16955f96d1ca3df22c2
krb5-libs-1.18.2-28.el8_10.x86_64
=============================================

===== Searching CPO Image Version 4.15 =====
Image: quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:9be692cbde55532fe89fc4e45ade123959c2f09c12d8b6f2274e6be18e815fcd
quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:9be692cbde55532fe89fc4e45ade123959c2f09c12d8b6f2274e6be18e815fcd
krb5-libs-1.20.1-9.el9_2.x86_64
=============================================

===== Searching CPO Image Version 4.16 =====
Image: quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:aaf20d8da39383e8e31677368c1d89f938c56d4f4e70cb0a51172d650604e45d
quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:aaf20d8da39383e8e31677368c1d89f938c56d4f4e70cb0a51172d650604e45d
krb5-libs-1.20.1-9.el9_2.x86_64
=============================================

===== Searching CPO Image Version 4.17 =====
Image: quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:e50f438a9b6a4e6881eda977a4e4293d414b3f2abb5249837575fc30942c36fa
quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:e50f438a9b6a4e6881eda977a4e4293d414b3f2abb5249837575fc30942c36fa
krb5-libs-1.21.1-1.el9.x86_64
=============================================

Search completed.
```




