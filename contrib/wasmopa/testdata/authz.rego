package authz

import rego.v1

default allow := false

allow if {
    input.subject.roles[_] == "admin"
}

allow if {
    input.action == "read"
    input.subject.roles[_] == "viewer"
}
