# Requirements Document

## Introduction

This feature adds authentication capability to private GitHub Container Registry (ghcr.io) in DockPal. Users can store credentials in the form of a GitHub Personal Access Token (PAT) to pull images from their private registry. This mechanism is similar to registry credential management in Portainer and Dokploy — there is a UI to input the token, and the application can pull private images automatically.

## Glossary

- **DockPal**: A web-based Docker management application with a Go backend and HTML/JS frontend
- **Registry_Credential**: A data object that stores authentication information for accessing a private container registry, consisting of registry name, username, and token
- **PAT**: Personal Access Token from GitHub with the format `ghp_` followed by alphanumeric characters, used as authentication credentials for ghcr.io
- **GHCR**: GitHub Container Registry, a container registry service from GitHub accessed via the ghcr.io domain
- **Credential_Store**: A storage bucket in the BoltDB database that stores Registry_Credentials in encrypted form
- **Registry_Manager**: A backend module that manages CRUD operations and validation for Registry_Credentials
- **Image_Puller**: A component that pulls images from a registry by including authentication from stored Registry_Credentials

## Requirements

### Requirement 1: Store Registry Credentials

**User Story:** As a DockPal user, I want to store GitHub PAT credentials for ghcr.io, so that I can pull images from my private registry.

#### Acceptance Criteria

1. WHEN a user submits new registry credentials via the API, THE Registry_Manager SHALL store the Registry_Credential to the Credential_Store with the token encrypted and return a success response containing the created credential ID and registry name within a maximum of 2 seconds
2. WHEN a user submits credentials with an invalid PAT format (not prefixed with `ghp_` or total length is not 40 alphanumeric characters), THE Registry_Manager SHALL reject the request and return an error message explaining the expected format: `ghp_` followed by 36 alphanumeric characters
3. WHEN a user submits credentials without required fields (registry URL, username, or token), THE Registry_Manager SHALL reject the request with status 400 and a specific error message per empty field
4. THE Registry_Manager SHALL store each Registry_Credential with a unique ID, registry name (maximum 253 characters), username (maximum 100 characters), encrypted token, and creation timestamp in Unix epoch format
5. WHEN a user submits credentials with a registry name that already exists (case-insensitive matching), THE Registry_Manager SHALL update the existing credential (token and username) instead of creating a duplicate, and update the modification timestamp
6. IF the Credential_Store fails to save data due to a database error during credential creation, THEN THE Registry_Manager SHALL return an error message indicating a storage failure without exposing internal database details
7. IF the Credential_Store fails to save data due to a database error during credential update, THEN THE Registry_Manager SHALL return a specific error message indicating the update failed and the existing credential remains unchanged

### Requirement 2: Manage Registry Credentials

**User Story:** As a DockPal user, I want to view, modify, and delete stored registry credentials, so that I can manage access to my private registries.

#### Acceptance Criteria

1. WHEN a user requests the list of credentials, THE Registry_Manager SHALL return all stored Registry_Credentials with tokens displayed in masked form showing only the last 4 characters of the original token
2. WHEN a user requests details of a single credential, THE Registry_Manager SHALL return credential information (ID, registry name, username, creation timestamp, update timestamp) with the token masked using the format: last 4 characters displayed, remainder replaced with asterisk characters
3. WHEN a user deletes a credential, THE Registry_Manager SHALL permanently remove the Registry_Credential from the Credential_Store and return a deletion confirmation only after successful removal from storage
4. WHEN a user updates the token on an existing credential, THE Registry_Manager SHALL validate the new token format according to PAT format (`ghp_` prefix followed by alphanumeric characters), store the new token encrypted, and update the modification timestamp
5. IF a user requests details, updates, or deletes a credential with an ID not found in the Credential_Store, THEN THE Registry_Manager SHALL return an error message explaining that the credential was not found

### Requirement 3: Pull Image with Registry Authentication

**User Story:** As a DockPal user, I want to pull images from private ghcr.io using stored credentials, so that I can deploy containers from my private registry.

#### Acceptance Criteria

1. WHEN a user pulls an image from ghcr.io, THE Image_Puller SHALL extract the domain from the image reference, match it via exact-match (case-insensitive) with the registry name on stored Registry_Credentials, and include authentication automatically
2. WHEN credentials for the requested registry are not found in the Credential_Store, THE Image_Puller SHALL perform the pull without authentication (fallback to public) and return a status indicating the pull was performed without credentials
3. IF authentication to the registry fails due to an invalid or expired token (HTTP 401 or 403), THEN THE Image_Puller SHALL return an error message explaining that the credentials need to be updated along with the problematic registry name, regardless of any internal authentication state
4. WHEN an image pull succeeds with authentication, THE Image_Puller SHALL return a success status along with pulled image information (full name, tag, and digest)
5. IF an image pull fails due to a network issue (timeout or host unreachable), THEN THE Image_Puller SHALL return an error message that distinguishes between network issues and authentication issues

### Requirement 4: Validate Registry Connection

**User Story:** As a DockPal user, I want to validate that my registry credentials are working correctly, so that I can ensure access before deploying.

#### Acceptance Criteria

1. WHEN a user requests credential validation, THE Registry_Manager SHALL attempt authentication to the target registry with a maximum timeout of 30 seconds and return the connection status (success or failure)
2. IF the connection to the registry fails, THEN THE Registry_Manager SHALL return an error message that categorizes the failure cause into one of: network issue (timeout or host unreachable), invalid credentials (token rejected by registry), or registry unavailable (server error from registry)
3. WHEN validation succeeds, THE Registry_Manager SHALL return information that the credentials are valid along with the validation timestamp and store that timestamp on the Registry_Credential in the Credential_Store
4. IF the credential requested for validation is not found in the Credential_Store, THEN THE Registry_Manager SHALL return an error message explaining that the credential was not found

### Requirement 5: Token Storage Security

**User Story:** As a DockPal user, I want my tokens stored securely, so that credentials do not leak if the database is accessed by unauthorized parties.

#### Acceptance Criteria

1. THE Credential_Store SHALL store tokens using AES-256-GCM encryption with a cryptographically generated unique nonce (crypto/rand) for each encryption operation before writing to the database
2. THE Registry_Manager SHALL decrypt tokens only when needed for registry authentication, zero-out the plaintext token from memory before the function using it completes execution, and use deferred cleanup mechanisms to ensure memory is zeroed even on abnormal function termination
3. IF the encryption key is unavailable or corrupted, THEN THE Registry_Manager SHALL reject all operations requiring token access and return a generic error message indicating an encryption configuration failure without exposing the key value, ciphertext, or any diagnostic details in logs or responses
4. THE Credential_Store SHALL use an encryption key derived from the application's JWT secret using HKDF with a dedicated context string to ensure isolation from the JWT signing key
5. IF token decryption fails due to corrupted or modified ciphertext, THEN THE Credential_Store SHALL return an error indicating that the stored credential cannot be decrypted and needs to be re-saved

### Requirement 6: Registry Management User Interface

**User Story:** As a DockPal user, I want a dedicated UI page to manage registry credentials, so that I can add, view, and delete credentials easily.

#### Acceptance Criteria

1. THE DockPal SHALL provide a "Registry" page accessible from the sidebar navigation
2. WHEN a user opens the Registry page, THE DockPal SHALL display a list of all stored credentials in table format with columns: registry name, username, connection status (valid, invalid, or not yet validated), and creation date
3. WHEN a user clicks the "Add Registry" button, THE DockPal SHALL display a form with fields: registry URL (default: ghcr.io, maximum 253 characters), username (maximum 100 characters, required), and token (input type password, required, maximum 255 characters)
4. WHEN a user submits the form with empty required fields or exceeding character limits, THE DockPal SHALL display an error message on the problematic field without sending a request to the server
5. WHEN a user clicks the "Test Connection" button on the form, THE DockPal SHALL validate the credentials with a 15-second timeout and display a success indicator (green) or failure indicator (red) next to the button
6. WHEN a user clicks the delete button on a credential, THE DockPal SHALL display a confirmation dialog before deleting, and display a toast notification after successful deletion
7. WHILE the form is processing a request, THE DockPal SHALL display a loading indicator and disable the submit button
8. WHEN the Registry page has no stored credentials, THE DockPal SHALL hide the credentials table entirely and display an empty state message informing that no credentials exist with a button to add new credentials
9. IF a credential addition or deletion request fails due to a server error, THEN THE DockPal SHALL display an error notification explaining that the operation failed and data remains unchanged

### Requirement 7: Integration with Deploy Flow

**User Story:** As a DockPal user, I want the deploy process to automatically use stored registry credentials, so that I don't need to enter a token every time I deploy a private image.

#### Acceptance Criteria

1. WHEN a compose deploy process requires an image from a private registry, THE Image_Puller SHALL extract the domain from the image reference and match it via exact-match with the registry name on Registry_Credentials stored in the Credential_Store, then include those credentials in the pull request
2. IF a deploy fails because an image is not found and credentials are available for that registry in the Credential_Store, THEN THE DockPal SHALL include an error message informing that the credentials for that registry may be expired or do not have access to the requested image
3. THE Image_Puller SHALL parse image references with the format: `<registry>/<owner>/<image>:<tag>` and `<registry>/<owner>/<image>` (default tag `latest`), where registry is a domain containing a dot (e.g., ghcr.io)
4. IF there is more than one Registry_Credential matching the same domain, THEN THE Image_Puller SHALL use the most recently updated credential (based on the newest timestamp)
