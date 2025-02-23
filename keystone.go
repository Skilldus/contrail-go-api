//
// Copyright (c) 2014 Juniper Networks, Inc. All rights reserved.
//

package contrail

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"
)

// KeystoneClient is a client of the OpenStack Keystone service that adds authentication
// tokens to the Contrail API requests.
type KeystoneClient struct {
	osAuthURL           string
	osTenantName        string
	osUsername          string
	osPassword          string
	osAdminToken        string
	osDomainName        string
	osProjectName       string
	osProjectDomainName string
	current             *KeystoneToken
	httpClient          *http.Client
	tokenID             string
	isv3Client          bool
	issuedAt            string
	expiresAt           string
}

// KeepaliveKeystoneClient embeds KeystoneClient
type KeepaliveKeystoneClient struct {
	KeystoneClient
}

// KeystoneToken represents an auth token issued by OpenStack keystone service.
// The field names are defined by the Keystone API schema.
type KeystoneToken struct {
	Id      string
	Expires string
	Tenant  struct {
		Id          string
		Name        string
		Description string
		Enabled     bool
	}
	Issued_At string
}

type KeystoneTokenv3 struct {
	Token struct {
		ExpiresAt string `json:"expires_at"`
		IssuedAt  string `json:"issued_at"`
	} `json:"token"`
}

// NewKeystoneClient allocates and initializes a KeystoneClient
func NewKeystoneClient(auth_url, tenant_name, username, password, token, domain_name, project_name, project_domain_name string) *KeystoneClient {
	return &KeystoneClient{
		osAuthURL:           auth_url,
		osTenantName:        tenant_name,
		osUsername:          username,
		osPassword:          password,
		osAdminToken:        token,
		osDomainName:        domain_name,
		osProjectName:       project_name,
		osProjectDomainName: project_domain_name,
		current:             nil,
		httpClient:          &http.Client{},
	}
}

// NewKeepaliveKeystoneClient allocates and initializes a KeepaliveKeystoneClient
func NewKeepaliveKeystoneClient(auth_url, tenant_name, username, password, token, domain_name string) *KeepaliveKeystoneClient {
	return &KeepaliveKeystoneClient{
		KeystoneClient{
			osAuthURL:    auth_url,
			osTenantName: tenant_name,
			osUsername:   username,
			osPassword:   password,
			osAdminToken: token,
			osDomainName: domain_name,
			current:      nil,
			httpClient:   &http.Client{},
		},
	}
}

// Authenticate sends an authentication request to keystone.
func (kClient *KeystoneClient) AuthenticateV3() error {
	kClient.isv3Client = true
	type AuthCredentialsRequestv3 struct {
		Auth struct {
			Identity struct {
				Methods  []string `json:"methods"`
				Password struct {
					User struct {
						Domain struct {
							Name string `json:"name"`
						} `json:"domain"`
						Name     string `json:"name"`
						Password string `json:"password"`
					} `json:"user"`
				} `json:"password"`
			} `json:"identity"`
			Scope struct {
				Project struct {
					Name   string `json:"name"`
					Domain struct {
						Name string `json:"name"`
					} `json:"domain"`
				} `json:"project"`
			} `json:"scope"`
		} `json:"auth"`
	}

	url := kClient.osAuthURL
	if url[len(url)-1] != '/' {
		url += "/"
	}
	url += "v3/auth/tokens"

	var data []byte
	var err error
	request := AuthCredentialsRequestv3{}
	request.Auth.Identity.Password.User.Name = kClient.osUsername
	request.Auth.Identity.Password.User.Password = kClient.osPassword
	request.Auth.Identity.Password.User.Domain.Name = kClient.osDomainName
	request.Auth.Identity.Methods = append(request.Auth.Identity.Methods, "password")
	request.Auth.Scope.Project.Name = kClient.osProjectName
	request.Auth.Scope.Project.Domain.Name = kClient.osProjectDomainName

	if data, err = json.Marshal(&request); err != nil {
		return err
	}

	// Encryption for insecure access only here
	kClient.AddEncryption("", "", "", true)

	resp, err := kClient.httpClient.Post(url, "application/json",
		bytes.NewReader(data))

	if err != nil {
		return err
	}

	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("%s: %s", resp.Status, body)
	}

	var response KeystoneTokenv3
	err = json.Unmarshal(body, &response)

	if err != nil {
		return err
	}
	kClient.tokenID = resp.Header.Get("X-Subject-Token")
	kClient.issuedAt = response.Token.IssuedAt
	kClient.expiresAt = response.Token.ExpiresAt
	return nil

}

// Authenticate sends an authentication request to keystone.
func (kClient *KeystoneClient) Authenticate() error {
	// identity:CredentialType
	type AuthTokenRequest struct {
		Auth struct {
			Token struct {
				Id string `json:"id"`
			} `json:"token"`
		} `json:"auth"`
	}
	type AuthCredentialsRequest struct {
		Auth struct {
			TenantName          string `json:"tenantName"`
			PasswordCredentials struct {
				Username string `json:"username"`
				Password string `json:"password"`
			} `json:"passwordCredentials"`
		} `json:"auth"`
	}

	// identity-api/v2.0/src/xsd/token.xsd
	// <element name="access" type="identity:AuthenticateResponse"/>
	type TokenResponse struct {
		Access struct {
			Token KeystoneToken
			User  struct {
				Id       string
				Username string
			}
			// ServiceCatalog
		}
	}
	url := kClient.osAuthURL
	if url[len(url)-1] != '/' {
		url += "/"
	}
	url += "tokens"

	var data []byte
	var err error
	if len(kClient.osAdminToken) > 0 {
		request := AuthTokenRequest{}
		request.Auth.Token.Id = kClient.osAdminToken
		data, err = json.Marshal(&request)
	} else {
		request := AuthCredentialsRequest{}
		request.Auth.PasswordCredentials.Username =
			kClient.osUsername
		request.Auth.PasswordCredentials.Password =
			kClient.osPassword
		request.Auth.TenantName = kClient.osTenantName
		data, err = json.Marshal(&request)
	}

	if err != nil {
		return err
	}

	resp, err := kClient.httpClient.Post(url, "application/json",
		bytes.NewReader(data))

	if err != nil {
		return err
	}

	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", resp.Status, body)
	}

	var response TokenResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return err
	}

	kClient.expiresAt = response.Access.Token.Expires
	kClient.issuedAt = response.Access.Token.Issued_At
	kClient.tokenID = response.Access.Token.Id
	return nil
}

func (kClient *KeepaliveKeystoneClient) needsRefreshing() (bool, error) {
	if kClient.tokenID == "" {
		return true, nil
	}

	issuedAtTime, err := time.Parse(time.RFC3339, kClient.issuedAt)
	if err != nil {
		return false, err
	}

	expires, err := time.Parse(time.RFC3339, kClient.expiresAt)
	if err != nil {
		return false, err
	}

	refreshTime := issuedAtTime.UTC().Add(expires.UTC().Sub(issuedAtTime.UTC()) / 2)

	return time.Now().UTC().After(refreshTime.UTC()), nil
}

// AddAuthentication adds authentication token to the HTTP header of the KeepaliveKeystoneClient
func (kClient *KeepaliveKeystoneClient) AddAuthentication(req *http.Request) error {
	needsRefreshing, err := kClient.needsRefreshing()
	if err != nil {
		return err
	}

	if needsRefreshing {
		kClient.tokenID = ""
	}

	return kClient.KeystoneClient.AddAuthentication(req)
}

// AddAuthentication adds the authentication token to the HTTP header.
func (kClient *KeystoneClient) AddAuthentication(req *http.Request) error {
	if kClient.tokenID == "" {
		if kClient.isv3Client {
			if err := kClient.AuthenticateV3(); err != nil {
				return err
			}
		} else {
			if err := kClient.Authenticate(); err != nil {
				return err
			}
		}
	}
	req.Header.Set("X-Auth-Token", kClient.tokenID)
	return nil
}

// AddEncryption implements the Encryptor interface for Client.
func (kClient *KeystoneClient) AddEncryption(caFile string, keyFile string, certFile string, insecure bool) error {
	if !strings.HasPrefix(kClient.osAuthURL, "https") {
		kClient.osAuthURL = strings.Replace(kClient.osAuthURL, "http", "https", 1)
	}

	customTransport := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		customTransport.TLSClientConfig.InsecureSkipVerify = true
	} else if caFile != "" {
		caCert, err := ioutil.ReadFile(caFile)
		if err != nil {
			return nil
		}
		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)
		customTransport.TLSClientConfig.RootCAs = caCertPool
		if certFile != "" && keyFile != "" {
			cert, err := tls.LoadX509KeyPair(certFile, keyFile)
			if err != nil {
				return nil
			}
			customTransport.TLSClientConfig.Certificates = []tls.Certificate{cert}
		}
	}
	kClient.httpClient.Transport = customTransport

	return nil
}
