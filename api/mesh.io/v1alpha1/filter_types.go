package v1alpha1

// WorkloadOwnerAccessType represents workload owner access permission
// +kubebuilder:validation:Enum=allowed
type WorkloadOwnerAccessType string

const (
	// WorkloadOwnerAccessAllowed grants workload owner access
	WorkloadOwnerAccessAllowed WorkloadOwnerAccessType = "allowed"
)

type ExtensionElement struct {
	FaultFilter                  *HttpFaultFilter            `json:"faultInjection,omitempty"`
	PayloadStats                 *StatsFilter                `json:"payloadStats,omitempty"`
	GatewayFilter                *GatewayProxyFilter         `json:"gatewayProxy,omitempty"`
	Telemetry                    *TelemetryConfig            `json:"telemetry,omitempty"`
	MaxOutboundFrames            *MaxOutboundFrames          `json:"maxOutboundFrames,omitempty"`
	ServerMaxOutboundFrames      *ServerMaxOutboundFrames    `json:"serverMaxOutboundFrames,omitempty"`
	LocalRateLimitFilter         *LocalRateLimitFilter       `json:"localRateLimit,omitempty"`
	GRPCJsonTranscoderFilter     *GRPCJsonTranscoderFilter   `json:"grpcJsonTranscoder,omitempty"`
	ActiveHealthCheckFilter      *ActiveHealthCheck          `json:"activeHealthCheck,omitempty"`
	Authority                    *AuthorityFilter            `json:"authority,omitempty"`
	GzipFilter                   *GzipFilter                 `json:"gzip,omitempty"`
	OAuth2Filter                 *OAuth2Filter               `json:"oauth2,omitempty"`
	MixedMode                    *MixedMode                  `json:"mixedMode,omitempty"`
	HealthCheckFilter            *HealthCheckFilter          `json:"healthCheck,omitempty"`
	LoadBalancerConfig           *LoadBalancerConfig         `json:"loadBalancerConfig,omitempty"`
	GlobalRateLimitFilter        *GlobalRateLimitFilter      `json:"globalRateLimit,omitempty"`
	IngressGlobalRateLimitFilter *IngressRateLimitFilter     `json:"ingressRateLimit,omitempty"`
	IngressRequestHeaders        *IngressRequestHeaders      `json:"ingressRequestHeaders,omitempty"`
	IngressResponseHeaders       *IngressResponseHeaders     `json:"ingressResponseHeaders,omitempty"`
	IngressCookieAttributes      *IngressCookieAttribute     `json:"ingressCookieAttributes,omitempty"`
	IngressLua                   *IngressLua                 `json:"ingressLua,omitempty"`
	MeshRequestHeaders           *MeshRequestHeaders         `json:"meshRequestHeaders,omitempty"`
	MeshRequestHeadersPerRoute   *MeshRequestHeadersPerRoute `json:"meshRequestHeadersPerRoute,omitempty"`
	ClusterConfigServerSide      *ClusterConfigServerSide    `json:"clusterConfigServerSide,omitempty"`
	ClusterConfigClientSide      *ClusterConfigClientSide    `json:"clusterConfigClientSide,omitempty"`
	SparkConfiguration           *SparkConfiguration         `json:"sparkConfiguration,omitempty"`
	ProxyProtocolFilter          *ProxyProtocolFilter        `json:"proxyProtocol,omitempty"`
	SimpleTlsDowngrade           *SimpleTlsDowngrade         `json:"simpleTlsDowngrade,omitempty"`
}

// HTTPHealthCheck - Health check configuration for HTTP
type HTTPHealthCheck struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=\/.+
	Path string `json:"path"`
}

// RedisHealthCheck - Health check configuration for Redis using key-based EXISTS query
type RedisHealthCheck struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength:=1
	Key string `json:"key"`
}

// ActiveHealthCheck - Enable active health-checking
type ActiveHealthCheck struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems:=1
	PortLevelHealthChecks  []PortLevelHealthCheck `json:"portLevelHealthChecks"`
	ConfigNamespaceEnabled string                 `json:"-"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=false
	ForIngress bool `json:"forIngress"`

	// +kubebuilder:validation:Optional
	ActivateExtensionLabels map[string]string `json:"activateExtensionLabels,omitempty"`
}

// PortLevelHealthCheck - Configures active health-checking for the given port
type PortLevelHealthCheck struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum:=1
	Port int32 `json:"port"`

	// +kubebuilder:validation:Required
	HealthCheckerConfig *HealthCheckerConfig `json:"healthCheck"`
}

// HealthCheckerConfig - Active health check configuration object
type HealthCheckerConfig struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum:=1
	IntervalSec int32 `json:"intervalSec"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=1000
	// +kubebuilder:validation:Minimum:=1
	TimeoutMillis int32 `json:"timeoutMillis"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=1
	// +kubebuilder:validation:Minimum:=1
	NoTrafficIntervalMins int32 `json:"noTrafficIntervalMins"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=2
	// +kubebuilder:validation:Minimum:=1
	HealthyThreshold int32 `json:"successThreshold"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=3
	// +kubebuilder:validation:Minimum:=1
	UnhealthyThreshold int32 `json:"failureThreshold"`

	// +kubebuilder:validation:Optional
	HTTPHealthCheck *HTTPHealthCheck `json:"httpHealthCheck,omitempty"`

	// +kubebuilder:validation:Optional
	RedisHealthCheck *RedisHealthCheck `json:"redisHealthCheck,omitempty"`
}

type HttpFaultFilter struct {
	// +kubebuilder:default:=false
	ClientFault bool `json:"clientFault,omitempty"`

	Abort      *HttpFaultFilterAbort  `json:"abort,omitempty"`
	RedisAbort *RedisFaultFilterAbort `json:"redisAbort,omitempty"`
	Delay      *HttpFaultFilterDelay  `json:"delay,omitempty"`
	RedisDelay *RedisFaultFilterDelay `json:"redisDelay,omitempty"`
	Ports      FaultFilterPorts       `json:"ports,omitempty"`
}

// +kubebuilder:validation:Enum={HUNDRED,TEN_THOUSAND,MILLION}
type PercentageDenominator string

type FaultFilterPorts []int32

type Percentage struct {
	//+kubebuilder:default:=100
	Numerator *int `json:"numerator,omitempty"`
	//+kubebuilder:default:=HUNDRED
	Denominator *PercentageDenominator `json:"denominator,omitempty"`
}

type HttpFaultFilterAbort struct {
	// +kubebuilder:validation:Minimum=200
	// +kubebuilder:validation:Maximum=599
	HttpStatus int32 `json:"httpStatus,omitempty"`
	GrpcStatus int32 `json:"grpcStatus,omitempty"`
	//+kubebuilder:default:={numerator:100,denominator:"HUNDRED"}
	Percentage *Percentage `json:"percentage,omitempty"`
}

type HttpFaultFilterDelay struct {
	FixedDelay string `json:"fixedDelay"`
	//+kubebuilder:default:={numerator:100,denominator:"HUNDRED"}
	Percentage *Percentage `json:"percentage,omitempty"`
}

type RedisFaultFilterAbort struct {
	//+kubebuilder:default:={numerator:100,denominator:"HUNDRED"}
	Percentage *Percentage `json:"percentage,omitempty"`

	// +kubebuilder:validation:Optional
	Commands []string `json:"commands,omitempty"`
}

type RedisFaultFilterDelay struct {
	FixedDelay string `json:"fixedDelay"`
	//+kubebuilder:default:={numerator:100,denominator:"HUNDRED"}
	Percentage *Percentage `json:"percentage,omitempty"`

	// +kubebuilder:validation:Optional
	Commands []string `json:"commands,omitempty"`
}

// StatsFilter - Enable cluster stats
// https://www.envoyproxy.io/docs/envoy/latest/api-v3/config/cluster/v3/cluster.proto#config-cluster-v3-trackclusterstats
type StatsFilter struct {
	// +kubebuilder:validation:Optional
	Outbound *OutboundStats `json:"outbound"`
	// +kubebuilder:validation:Optional
	Inbound *InboundStats `json:"inbound"`
}

// OutboundStats - outbound cluster is matched with k8s service FQDN
type OutboundStats struct {
	// +kubebuilder:validation:Required
	ServiceFQDN string `json:"serviceFQDN"`
}

// InboundStats - inbound cluster is matched with k8s service ports
type InboundStats struct {
	// +kubebuilder:validation:Optional
	Ports []int32 `json:"ports"`
}

// GatewayProxyFilter - Enables sidecar to act as another gateway behind ingress gateway so that it can do intelligent routing in the cluster. This enables features such as retaining the original client in xfcc header, etc.
type GatewayProxyFilter struct {
	// +kubebuilder:default:=false
	Enabled bool `json:"enabled"`
}

// TelemetryConfig - Allows configuration of telemetry API
type TelemetryConfig struct {
	// +kubebuilder:validation:Optional
	Tracing *TracingConfig `json:"tracing"`
	// +kubebuilder:validation:Optional
	AccessLog *AccessLogConfig `json:"accessLog"`
}

// +kubebuilder:validation:Enum={CLIENT_AND_SERVER,CLIENT,SERVER}
type WorkloadMode string

// TracingConfig - Telemetry tracing config
type TracingConfig struct {
	//+kubebuilder:default:={numerator:1,denominator:"HUNDRED"}
	SamplingPercentage *Percentage `json:"samplingPercentage,omitempty"`
	//+kubebuilder:default:=CLIENT_AND_SERVER
	WorkloadMode *WorkloadMode `json:"mode,omitempty"`
}

// AccessLogConfig - Telemetry access log config
type AccessLogConfig struct {
	// +kubebuilder:validation:Required
	Enabled bool `json:"enabled"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=false
	Minimal bool `json:"minimal"`
}

// MaxOutboundFrames - HTTP2 max-outbound-frames configs
type MaxOutboundFrames struct {
	// +kubebuilder:validation:Required
	Port int32 `json:"port"`
	// +kubebuilder:default:=10000
	// +kubebuilder:validation:Minimum:=10000
	MaxFrames              int32  `json:"maxFrames"`
	ConfigNamespaceEnabled string `json:"-"`
}

// ServerMaxOutboundFrames - HTTP2 max-outbound-frames configs
type ServerMaxOutboundFrames struct {
	// +kubebuilder:validation:Required
	Port int32 `json:"port"`
	// +kubebuilder:default:=10000
	// +kubebuilder:validation:Minimum:=10000
	MaxFrames int32 `json:"maxFrames"`
}

// KeyValuePair - A key-value pair
type KeyValuePair struct {
	// +kubebuilder:validation:Required
	Key string `json:"key"`
	// +kubebuilder:validation:Required
	Value string `json:"value"`
}

// Service - Caller service to which rate limit should be applied
type Service struct {
	// +kubebuilder:validation:Required
	ServiceIdentity string `json:"serviceIdentity"`

	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`

	// +kubebuilder:validation:Optional
	SpiffeElements []KeyValuePair `json:"spiffeElements"`
}

// RateLimitHeaderMatch - key-value pairs for which rate limit should be applied
type RateLimitHeaderMatch struct {
	// Require either header+value or header+regex
	// make sure to restore the CRD generated by controller-gen
	// +kubebuilder:validation:Required
	Header string `json:"header"`
	// +kubebuilder:validation:Optional
	Value *string `json:"value"`
	// +kubebuilder:validation:Optional
	Regex *string `json:"regex"`
}

// FilteredRateLimit - Rate limit configuration and match criteria
type FilteredRateLimit struct {
	// +kubebuilder:validation:Optional
	Name string `json:"name"`

	// +kubebuilder:validation:Optional
	Caller *Service `json:"caller"`

	// +kubebuilder:validation:Optional
	HeaderMatches []RateLimitHeaderMatch `json:"headers"`

	// +kubebuilder:validation:Required
	RateLimit RateLimit `json:"rateLimit"`
}

// RateLimit - Parameters used in limit calculation
type RateLimit struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum={SECOND,MINUTE,HOUR,DAY}
	RateLimitUnit string `json:"unit"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=1
	// +kubebuilder:validation:Minimum=1
	UnitFactor int32 `json:"unitFactor"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	MaxRequests int32 `json:"maxRequests"`
}

// PortRateLimit - Port-based rate limit configuration
type PortRateLimit struct {
	// +kubebuilder:validation:Optional
	Name string `json:"name"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	Port int32 `json:"port"`

	// +kubebuilder:validation:Optional
	RateLimit *RateLimit `json:"rateLimit"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinItems=1
	FilteredRateLimits []FilteredRateLimit `json:"filteredRateLimits"`
}

// LocalRateLimitFilter - Enable local (per sidecar) rate limiting
type LocalRateLimitFilter struct {
	// +kubebuilder:validation:Optional
	PortRateLimits []PortRateLimit `json:"portRateLimits"`
}

// GRPCJsonTranscoderFilter is a filter which allows a RESTful JSON API client to send requests to Envoy
// over HTTP and get proxied to a gRPC service.
type GRPCJsonTranscoderFilter struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	Port int32 `json:"port"`

	// +kubebuilder:validation:Required
	ProtoDescriptor string `json:"protoDescriptor"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Services []string `json:"services"`
}

// AuthorityFilter - Authority filter config
type AuthorityFilter struct {
	// +kubebuilder:default:=false
	Enabled bool `json:"enabled"`
}

type Port int32

type GzipContextConfig struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Ports []Port `json:"ports"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinItems=1
	ContentTypes []string `json:"contentTypes"`
}

// GzipFilter - enables a client to request compression of data dispatched by an upstream service
type GzipFilter struct {
	InboundConfig  *GzipContextConfig `json:"inboundConfig"`
	OutboundConfig *GzipContextConfig `json:"outboundConfig"`
}

// OAuth2Filter - enables a client to support for OAuth2
type OAuth2Filter struct {
	// +kubebuilder:validation:Optional
	Ports []int32 `json:"ports"`

	// +kubebuilder:validation:Optional
	AuthScopes []string `json:"authScopes"`

	// +kubebuilder:validation:Required
	RedirectURI string `json:"redirectURI"`

	// +kubebuilder:validation:Optional
	AuthorizationRules []OAuth2AuthorizatonRule `json:"authorizationRules"`

	// +kubebuilder:default:=false
	// +kubebuilder:validation:Optional
	AllowAuthorizationHeader bool `json:"allowAuthorizationHeader,omitempty"`

	// +kubebuilder:default:=false
	// +kubebuilder:validation:Optional
	ForwardBearerToken bool `json:"forwardBearerToken,omitempty"`

	// +kubebuilder:validation:Optional
	Providers []Oauth2Provider `json:"providers"`

	// +kubebuilder:validation:Optional
	OauthClient OauthClient `json:"oauthClient"`

	// +kubebuilder:default:=false
	// +kubebuilder:validation:Optional
	AllowMtlsPassthrough bool `json:"allowMtlsPassthrough,omitempty"`
}

// +kubebuilder:validation:Enum={CONNECT, DELETE, GET, HEAD, OPTIONS, PATCH, POST, PUT, TRACE}
type HttpMethod string

// +kubebuilder:validation:Enum={EMPLOYEE, CONTINGENT, SERVICE_ACCOUNT}
type EmployeeType string

type OAuth2AuthorizatonRule struct {
	// +kubebuilder:validation:Optional
	Path string `json:"path,omitempty"`

	// +kubebuilder:validation:Optional
	RegexPath string `json:"rpath,omitempty"`

	// +kubebuilder:validation:Optional
	Methods []HttpMethod `json:"methods"`

	// +kubebuilder:validation:Optional
	EmployeeTypes []EmployeeType `json:"employeeTypes,omitempty"`

	// +kubebuilder:validation:Optional
	Groups []string `json:"groups,omitempty"`

	// +kubebuilder:validation:Optional
	WorkloadOwnerAccess *WorkloadOwnerAccessType `json:"workloadOwnerAccess,omitempty"`
}

type Oauth2Provider struct {
	// +kubebuilder:validation:Required
	Audiences []string `json:"audiences"`

	// +kubebuilder:validation:Required
	ServiceProvider string `json:"serviceProvider"`
}

type OauthClient struct {
	// +kubebuilder:validation:Required
	ClientId string `json:"clientId"`

	// +kubebuilder:validation:Required
	ServiceProvider string `json:"serviceProvider"`

	// +kubebuilder:default:=false
	// +kubebuilder:validation:Optional
	UseMtlsTokenEndpoint bool `json:"useMtlsTokenEndpoint,omitempty"`
}
type MixedMode struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Ports []TargetedPort `json:"ports"`
}

type TargetedPort struct {
	// +kubebuilder:validation:Required
	Port int32 `json:"port"`

	// +kubebuilder:validation:Required
	TargetPort int32 `json:"targetPort"`
}

type PortAndPath struct {
	// +kubebuilder:validation:Required
	Port int32 `json:"port"`

	// +kubebuilder:validation:Required
	Path string `json:"path"`
}

// HealthCheckFilter - Add a health check endpoint to a service
type HealthCheckFilter struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems:=1
	Endpoint []PortAndPath `json:"endpoint"`
}

// +kubebuilder:validation:Enum={UNKNOWN, HEALTHY, UNHEALTHY, DRAINING, TIMEOUT, DEGRADED}
type HostStatus string

type OverrideHostStatus struct {
	// +kubebuilder:validation:Required
	Statuses []HostStatus `json:"statuses"`
}

// LoadBalancerConfig - Common configurations for load balancer
type LoadBalancerConfig struct {
	ConfigNamespaceEnabled string `json:"-"`

	// +kubebuilder:validation:Optional
	Port *int32 `json:"port"`
	// +kubebuilder:validation:Optional
	OverrideHostStatus *OverrideHostStatus `json:"overrideHostStatus"`
}

// GlobalRateLimitFilter - Custom configurations for rate limit filter per inbound port
type GlobalRateLimitFilter struct {
	// +kubebuilder:validation:Optional
	RateLimitService *RateLimitService `json:"rateLimitService"`

	// +kubebuilder:validation:Required
	RateLimitConfigs []GlobalRateLimitConfig `json:"rateLimitConfigs"`

	// +kubebuilder:validation:Optional
	EnableXRatelimitHeaders bool `json:"enableXRatelimitHeaders"`
}

// IngressRateLimitFilter - Custom configurations for rate limit filter per inbound port at IG layer
type IngressRateLimitFilter struct {
	// +kubebuilder:validation:Optional
	RateLimitService *RateLimitService `json:"rateLimitService"`

	// +kubebuilder:validation:Required
	RateLimitConfigs []IngressRateLimitConfig `json:"rateLimitConfigs"`

	// +kubebuilder:validation:Optional
	EnableXRatelimitHeaders bool `json:"enableXRatelimitHeaders"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems:=1
	Hosts []string `json:"hosts"`

	// +kubebuilder:validation:Required
	RouteName string `json:"routeName"`

	IngressConfigNamespaceEnabled string `json:"-"`
}

type RateLimitService struct {
	// +kubebuilder:validation:Required
	ServiceName string `json:"serviceName"`
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	Port int32 `json:"port"`
	// +kubebuilder:validation:Optional
	Subset string `json:"subset"`
	// +kubebuilder:validation:Required
	Authority string `json:"authority"`
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:="5s"
	Timeout string `json:"timeout"`
}

// BaseRateLimitConfig - Common configuration fields for rate limiting
type BaseRateLimitConfig struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems:=1
	InboundPorts []int32 `json:"inboundPorts"`
	// +kubebuilder:validation:Required
	Domain string `json:"domain"`
	// +kubebuilder:default:=false
	// +kubebuilder:validation:Optional
	FailureModeDeny bool `json:"failureModeDeny,omitempty"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems:=1
	ActionRules []ActionRule `json:"actionRules"`
}

// GlobalRateLimitConfig - Configuration for global rate limiting with requestType support
type GlobalRateLimitConfig struct {
	BaseRateLimitConfig `json:",inline"`
	// +kubebuilder:default:="both"
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum={internal,external,both}
	RequestType string `json:"requestType,omitempty"`
}

// IngressRateLimitConfig - Configuration for ingress rate limiting without requestType support
type IngressRateLimitConfig struct {
	BaseRateLimitConfig `json:",inline"`
}

// ActionRule defines rules based on which an application want to rate limit a given request
type ActionRule struct {
	// +kubebuilder:validation:Required
	Actions []Action `json:"actions"`
}

type Action struct {
	// +kubebuilder:validation:Optional
	RequestHeaders *RequestHeaders `json:"requestHeaders,omitempty"`
	// +kubebuilder:validation:Optional
	GenericKey *GenericKey `json:"genericKey,omitempty"`
	// +kubebuilder:validation:Optional
	HeaderValueMatch *HeaderValueMatch `json:"headerValueMatch,omitempty"`
}

type RequestHeaders struct {
	// +kubebuilder:validation:Required
	HeaderName string `json:"headerName"`
	// +kubebuilder:validation:Required
	DescriptorKey string `json:"descriptorKey"`
	// +kubebuilder:validation:Optional
	SkipIfAbsent bool `json:"skipIfAbsent,omitempty"`
}

type GenericKey struct {
	// +kubebuilder:validation:Required
	DescriptorValue string `json:"descriptorValue"`
	// +kubebuilder:validation:Optional
	DescriptorKey string `json:"descriptorKey,omitempty"`
}

type HeaderValueMatch struct {
	// +kubebuilder:validation:Required
	DescriptorValue string `json:"descriptorValue"`
	// +kubebuilder:validation:Optional
	DescriptorKey string `json:"descriptorKey,omitempty"`
	// +kubebuilder:validation:Optional
	ExpectMatch bool `json:"expectMatch,omitempty"`
	// +kubebuilder:validation:Required
	Headers []Header `json:"headers"`
}

type Header struct {
	// +kubebuilder:validation:Required
	HeaderName string `json:"name"`

	// +kubebuilder:validation:Optional
	ExactMatch string `json:"exactMatch,omitempty"`
	// +kubebuilder:validation:Optional
	SafeRegexMatch *SafeRegexMatch `json:"safeRegexMatch,omitempty"`
	// +kubebuilder:validation:Optional
	RangeMatch *RangeMatch `json:"rangeMatch,omitempty"`
	// +kubebuilder:validation:Optional
	PresentMatch bool `json:"presentMatch,omitempty"`
	// +kubebuilder:validation:Optional
	PrefixMatch string `json:"prefixMatch,omitempty"`
	// +kubebuilder:validation:Optional
	SuffixMatch string `json:"suffixMatch,omitempty"`
	// +kubebuilder:validation:Optional
	ContainsMatch string `json:"containsMatch,omitempty"`
	// +kubebuilder:validation:Optional
	StringMatch *StringMatch `json:"stringMatch,omitempty"`
	// +kubebuilder:validation:Optional
	InvertMatch bool `json:"invertMatch,omitempty"`
	// +kubebuilder:validation:Optional
	TreatMissingHeaderAsEmpty bool `json:"treatMissingHeaderAsEmpty,omitempty"`
}

type SafeRegexMatch struct {
	// +kubebuilder:validation:Required
	Regex string `json:"regex"`
}

type RangeMatch struct {
	// +kubebuilder:validation:Required
	Start int64 `json:"start"`
	// +kubebuilder:validation:Required
	End int64 `json:"end"`
}

type StringMatch struct {
	// +kubebuilder:validation:Optional
	Exact string `json:"exact,omitempty"`
	// +kubebuilder:validation:Optional
	Prefix string `json:"prefix,omitempty"`
	// +kubebuilder:validation:Optional
	Suffix string `json:"suffix,omitempty"`
	// +kubebuilder:validation:Optional
	SafeRegex *SafeRegexMatch `json:"safeRegex,omitempty"`
	// +kubebuilder:validation:Optional
	Contains string `json:"contains,omitempty"`
	// +kubebuilder:validation:Optional
	IgnoreCase bool `json:"ignoreCase,omitempty"`
}

// MeshRequestHeaders - Mesh request header mutation
type MeshRequestHeaders struct {
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinItems=1
	Ports []int32 `json:"ports"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Mutations []HeaderMutation `json:"mutations"`
}

// MeshRequestHeadersPerRoute - Mesh request header mutation per route
type MeshRequestHeadersPerRoute struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Routes []Route `json:"routes"`
}

type Route struct {
	// +kubebuilder:validation:Required
	Port int32 `json:"port"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	PathMatches []StringMatch `json:"pathMatches"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Mutations []HeaderMutation `json:"mutations"`
}

// +kubebuilder:validation:Enum={80, 443, 7443, 9443, 10443, 11443}
type IgPort int32

// IngressRequestHeaders - Ingress request header mutation
type IngressRequestHeaders struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Ports []IgPort `json:"ports"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Mutations []HeaderMutation `json:"mutations"`

	IngressConfigNamespaceEnabled string `json:"-"`
}

// IngressResponseHeaders - Ingress response header mutation
type IngressResponseHeaders struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Ports []IgPort `json:"ports"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Mutations []HeaderMutation `json:"mutations"`

	IngressConfigNamespaceEnabled string `json:"-"`
}

type HeaderMutation struct {
	Remove          string       `json:"remove,omitempty"`
	AppendOrAdd     *HeaderValue `json:"appendOrAdd,omitempty"`
	AppendIfAbsent  *HeaderValue `json:"appendIfAbsent,omitempty"`
	ReplaceOrAdd    *HeaderValue `json:"replaceOrAdd,omitempty"`
	ReplaceIfExists *HeaderValue `json:"replaceIfExists,omitempty"`
}

type HeaderValue struct {
	Header   string `json:"header,omitempty"`
	RawValue string `json:"rawValue,omitempty"`
	Value    string `json:"value,omitempty"`
}

// IngressCookieAttribute - Set cookie attributes on IG port(s)
type IngressCookieAttribute struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems:=1
	Ports []int32 `json:"ports"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems:=1
	Cookies                       []Cookie `json:"cookies"`
	IngressConfigNamespaceEnabled string   `json:"-"`
}

// IngressLua - Apply lua script for service at ingress
type IngressLua struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Ports []IgPort `json:"ports"`

	// +kubebuilder:validation:Required
	Type string `json:"type"`

	IngressConfigNamespaceEnabled string `json:"-"`
}

type Cookie struct {
	// +kubebuilder:validation:Optional
	CookieName string `json:"cookieName,omitempty"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems:=1
	Attributes []Attribute `json:"attributes"`
}

type Attribute struct {
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// +kubebuilder:validation:Optional
	Value string `json:"value,omitempty"`
}

// ClusterConfigServerSide - Server side cluster settings that are configurable
type ClusterConfigServerSide struct {
	// connectionPoolPerDownstreamConnection - If set to true, Envoy will create a new connection pool for each downstream connection.
	// +kubebuilder:validation:Required
	ConnectionPoolPerDownstreamConnection bool `json:"connectionPoolPerDownstreamConnection"`

	// +kubebuilder:validation:MinItems:=1
	Ports []int32 `json:"ports"`
}

// ClusterConfigClientSide - Client side cluster settings that are configurable
type ClusterConfigClientSide struct {
	// connectionPoolPerDownstreamConnection - If set to true, Envoy will create a new connection pool for each downstream connection.
	// +kubebuilder:validation:Required
	ConnectionPoolPerDownstreamConnection bool `json:"connectionPoolPerDownstreamConnection"`

	// +kubebuilder:validation:MinItems:=1
	Ports []int32 `json:"ports"`

	ConfigNamespaceEnabled string `json:"-"`
}

// SparkConfiguration - inputs needed to generate static mesh config for Flowsnake services
type SparkConfiguration struct {
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=true
	Enabled bool `json:"enabled"`

	// +kubebuilder:validation:Optional
	Ports []PortConfig `json:"ports,omitempty"`

	// +kubebuilder:validation:Optional
	EgressHosts []string `json:"egressHosts,omitempty"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems:=1
	SelectorLabels []KeyValuePair `json:"selectorLabels"`
}

type PortConfig struct {
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// +kubebuilder:validation:Required
	Port int32 `json:"port"`
}

// ProxyProtocolFilter - Enable proxy protocol configuration for TCP proxying
type ProxyProtocolFilter struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// Port number to apply proxy protocol configuration
	Port int32 `json:"port"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default="10s"
	// Connection timeout for clusters
	ConnectTimeout string `json:"connectTimeout,omitempty"`
}

// SimpleTlsDowngrade -  Configures the sidecar to enforce SIMPLE TLS termination on the specified inbound ports
type SimpleTlsDowngrade struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Ports []int32 `json:"ports"`
}
