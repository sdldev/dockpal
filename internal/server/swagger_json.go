package server

// SwaggerJSON is the OpenAPI 3.0 specification for Dockpal API
const SwaggerJSON = `{
  "openapi": "3.0.0",
  "info": {
    "title": "Dockpal API",
    "description": "Simple & powerful Docker management platform API documentation.",
    "version": "2.0.0"
  },
  "servers": [
    {
      "url": "/api",
      "description": "Local server"
    }
  ],
  "paths": {
    "/login": {
      "post": {
        "summary": "Authenticate user",
        "description": "Logs in the user and sets JWT cookie.",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "required": ["username", "password"],
                "properties": {
                  "username": { "type": "string" },
                  "password": { "type": "string" }
                }
              }
            }
          }
        },
        "responses": {
          "200": {
            "description": "Login successful",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": {
                    "token": { "type": "string" }
                  }
                }
              }
            }
          },
          "401": { "description": "Invalid credentials" }
        }
      }
    },
    "/logout": {
      "post": {
        "summary": "Logout user",
        "description": "Logs out the user by clearing the authentication token.",
        "responses": {
          "200": { "description": "Logout successful" }
        }
      }
    },
    "/auth/reset-password": {
      "post": {
        "summary": "Reset admin password",
        "description": "Resets the administrator password. Requires authentication.",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "required": ["password"],
                "properties": {
                  "password": { "type": "string", "minLength": 4 }
                }
              }
            }
          }
        },
        "responses": {
          "200": { "description": "Password reset successful" },
          "401": { "description": "Unauthorized" }
        }
      }
    },
    "/registries": {
      "get": {
        "summary": "List registry credentials",
        "description": "Returns a list of docker registry credentials configured in Dockpal.",
        "responses": {
          "200": {
            "description": "Success",
            "content": {
              "application/json": {
                "schema": {
                  "type": "array",
                  "items": {
                    "type": "object",
                    "properties": {
                      "id": { "type": "string" },
                      "registry": { "type": "string" },
                      "username": { "type": "string" }
                    }
                  }
                }
              }
            }
          }
        }
      },
      "post": {
        "summary": "Add registry credential",
        "description": "Saves a new docker registry credential.",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "required": ["registry", "username", "token"],
                "properties": {
                  "registry": { "type": "string" },
                  "username": { "type": "string" },
                  "token": { "type": "string" }
                }
              }
            }
          }
        },
        "responses": {
          "200": { "description": "Credential created successfully" },
          "400": { "description": "Invalid input data" }
        }
      }
    },
    "/instances": {
      "get": {
        "summary": "List agent instances",
        "description": "Returns a list of remote Dockpal instances managed by this controller.",
        "responses": {
          "200": {
            "description": "Success",
            "content": {
              "application/json": {
                "schema": {
                  "type": "array",
                  "items": {
                    "type": "object",
                    "properties": {
                      "id": { "type": "string" },
                      "name": { "type": "string" },
                      "status": { "type": "string" }
                    }
                  }
                }
              }
            }
          }
        }
      }
    },
    "/system/update/status": {
      "get": {
        "summary": "System update status",
        "description": "Returns the running version, the latest cached upstream release, whether an update is available, and the current update state machine. Requires authentication (viewer+).",
        "responses": {
          "200": {
            "description": "Success",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": {
                    "current_version": { "type": "string" },
                    "latest_version": { "type": "string" },
                    "update_available": { "type": "boolean" },
                    "update_enabled": { "type": "boolean" },
                    "last_checked_at": { "type": "integer" },
                    "changelog": { "type": "string" },
                    "changelog_url": { "type": "string" },
                    "published_at": { "type": "integer" },
                    "state": { "type": "string", "enum": ["none", "requested", "running", "done", "failed"] },
                    "state_detail": { "type": "object" }
                  }
                }
              }
            }
          }
        }
      }
    },
    "/system/update/check": {
      "post": {
        "summary": "Check for system updates",
        "description": "Forces a fresh check against the upstream releases and returns the updated status. Admin only.",
        "responses": {
          "200": { "description": "Updated system update status" },
          "403": { "description": "Admin role required" }
        }
      }
    },
    "/system/update": {
      "post": {
        "summary": "Request a system update",
        "description": "Stages a system update to the given version. The panel writes a trigger file consumed by the privileged systemd updater, which swaps the binary and restarts the service. Admin only.",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "required": ["version"],
                "properties": {
                  "version": { "type": "string" }
                }
              }
            }
          }
        },
        "responses": {
          "202": { "description": "Update requested" },
          "400": { "description": "Invalid or missing version" },
          "403": { "description": "Admin role required" },
          "409": { "description": "An update is already in progress" }
        }
      }
    }
  }
}
`
