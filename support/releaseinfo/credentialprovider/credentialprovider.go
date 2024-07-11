package credentialprovider

type DockerCredentialProvider struct {
	AWS ECRDockerCredentialProvider
}

func NewDockerCredentialProvider() *DockerCredentialProvider {
	return &DockerCredentialProvider{
		AWS: NewECRDockerCredentialProvider(),
	}
}
