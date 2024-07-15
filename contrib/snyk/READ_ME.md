# General
This script was created to quickly look for relevant CVEs in either the HO or CPO in a release image during snyk duty. To run this locally:

1. Update the PULL_SECRETS_FILE env var as necessary in `check-for-cve-in-ho.sh` and `check-for-cve-in-cpo.sh`
2. Run this commands below in your terminal passing in the search strings like `'wget,cpython,gnutls,glib'` with no spaces
   1. For the example below, I was looking for `wget or cpython or gnutls or glib` in the CPO image of a 4.16 release image

## HO Example
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

## CPO Example
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

