package etcd

import "time"

// TODO: Add this API as a Go dependency

type OperatorConfig struct {
	CheckInterval      time.Duration `json:"check-interval"`
	UnhealthyMemberTTL time.Duration `json:"unhealthy-member-ttl"`

	Etcd     EtcdConfiguration `json:"etcd"`
	ASG      ASGConfig         `json:"asg"`
	Snapshot SnapshotConfig    `json:"snapshot"`
}

type EtcdConfiguration struct {
	AdvertiseAddress        string              `json:"advertise-address"`
	DataDir                 string              `json:"data-dir"`
	ClientTransportSecurity SecurityConfig      `json:"client-transport-security"`
	PeerTransportSecurity   SecurityConfig      `json:"peer-transport-security"`
	BackendQuota            int64               `json:"backend-quota"`
	AutoCompactionMode      string              `json:"auto-compaction-mode"`
	AutoCompactionRetention string              `json:"auto-compaction-retention"`
	InitACL                 *ACLConfig          `json:"init-acl,omitempty"`
	JWTAuthTokenConfig      *JWTAuthTokenConfig `json:"jwt-auth-token-config,omitempty"`
}

type SecurityConfig struct {
	CertFile      string `json:"cert-file"`
	KeyFile       string `json:"key-file"`
	CertAuth      bool   `json:"client-cert-auth"`
	TrustedCAFile string `json:"trusted-ca-file"`
	AutoTLS       bool   `json:"auto-tls"`
}

type ACLConfig struct {
	RootPassword *string `json:"rootPassword,omitempty"`
	Roles        []Role  `json:"roles"`
	Users        []User  `json:"users"`
}

type JWTAuthTokenConfig struct {
	SignMethod     string `json:"sign-method"`
	PrivateKeyFile string `json:"private-key-file"`
	PublicKeyFile  string `json:"public-key-file"`
	TTL            string `json:"ttl"`
}

type Role struct {
	Name        string       `json:"name"`
	Permissions []Permission `json:"permissions"`
}

type User struct {
	Name     string   `json:"name"`
	Password string   `json:"password"`
	Roles    []string `json:"roles"`
}

type Permission struct {
	Mode     string `json:"mode"`
	Key      string `json:"key"`
	RangeEnd string `json:"rangeEnd"`
	Prefix   bool   `json:"prefix"`
}

type ASGConfig struct {
	Provider string                 `json:"provider"`
	Params   map[string]interface{} `json:",inline"`
}

type SnapshotConfig struct {
	Interval time.Duration `json:"interval"`
	TTL      time.Duration `json:"ttl"`

	Provider string                 `json:"provider"`
	Params   map[string]interface{} `json:",inline"`
}
