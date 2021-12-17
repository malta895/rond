# How to configure your RBAC service

The concept behind this service is proxy the incoming request and check permissions.
To setup correctly this service you need to follow this steps

## 1) Environment variables

| name | type | default value | required | description |
| ---- | ---- | ------------- | -------- | ----------- |
| LOG_LEVEL | string | info | - | one of["info", "trace", "debug", "warning", "error"] |
| HTTP_PORT | string | 8080 | - | service port to expose service |
| TARGET_SERVICE_HOST | string | - | ✅ | target service to redirect  | 
| API_PERMISSIONS_FILE_PATH | string | - | - | file path where you can manually configure permissions for your API, this substitues the automatic documentation fetch performed by the service. [See the example](#api-permission-file) |
| TARGET_SERVICE_OAS_PATH | string | - | - | endpoint of sibling container to contact for retrieve schemas (es. localhost:3001) |
| OPA_MODULES_DIRECTORY | string | - | ✅ | folder path where you serve all opa module. this files will be used to evaluate policy. [See the example](#rego-examples) |
| USER_PROPERTIES_HEADER_KEY | string | miauserproperties | - | the request header name that contains the user properties |
| USER_GROUPS_HEADER_KEY | string | miausergroups | - | the request header name that contains the user groups |
| CLIENT_TYPE_HEADER_KEY | string | Client-Type | - | the request header name that contains the client type |
| DELAY_SHUTDOWN_SECONDS | int | 10 (seconds) | - | the sidecar graceful shutdown |           
| MONGODB_URL| string| - | - | url to connect to MongoDB |
| ROLES_COLLECTION_NAME | string | - | - | name of the role collection |
| BINDINGS_COLLECTION_NAME | string | - | - | name of the bindings collection |
| DELAY_SHUTDOWN_SECONDS | int | 10 (seconds) | - | the sidecar graceful shutdown |

## How to write a policy
The policies must be write in Rego language and they could use the input variable or our built-in function.

### Rego input
```
{
    "request": {
            "method":  String,
            "path":    String,
            "headers": Object {
                String: Array[String]
            },
            "query":   Object {
                String: Array[String]
            },
    },
    "user": {
            "properties": Object{
                // this object contains the user properties inserted by the authorization service in the request user properties platform header 
            },
            "groups": Array[String],
    },
    "clientType": String
}
```

:::caution
The headers keys are in canonical form (i.e. "x-api-key" become "X-Api-Key"). 
In order to read the headers in case-insensitive mode, you can use our built-in function [`get_header`](#get_header-built-in-function)
:::

### Get_Header Built-in function

```
output := get_header(headerKey: String, headers: Map[String]Array<String>) 
```

#### Description

The returned output is the first header value present in the `headers` map at key `headerKey`. If `headerKey` doesn't exist the output returned is an empty string.



### Policy examples
```
    package policies

    default api_key = false

    api_key {
        count(input.request.headers["X-Api-Key"]) != 0
    }
```

```
    package policies

    default api_key = false
    api_key {
        get_header("x-api-key", input.request.headers) != ""
    }
```

## API permission file

In the schema of the target service's API, it must be registered the permissions associated to the different paths.
In the following example the route /hello require the permission `api_key` to accept and redirect the GET requests:

```json
{
    "paths": {
         "/hello": {
            "get": {
                "x-permission": {
                    "allow": "api.key"
                }
            }
        }
    }
}
```

:::info
In case of composed permission name, it's possible to use the dot notation as in the example above, but in the rego policy the dot character will be replaced with underscore.
:::