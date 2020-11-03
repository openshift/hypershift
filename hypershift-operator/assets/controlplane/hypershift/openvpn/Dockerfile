FROM registry.access.redhat.com/ubi7/ubi
RUN yum install -y https://dl.fedoraproject.org/pub/epel/epel-release-latest-7.noarch.rpm && \
    yum install -y openvpn && \
    yum clean all
CMD ["/usr/sbin/openvpn"]
