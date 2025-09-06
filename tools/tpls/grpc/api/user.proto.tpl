syntax = "proto3";

package api.v1;

option go_package = "{{.Module}}/api/v1";

import "google/api/annotations.proto";

// 用户服务定义
service UserService {
  // 创建用户
  rpc CreateUser(CreateUserRequest) returns (CreateUserResponse) {
    option (google.api.http) = {
      post: "/api/v1/users"
      body: "*"
    };
  }
  
  // 获取用户
  rpc GetUser(GetUserRequest) returns (GetUserResponse) {
    option (google.api.http) = {
      get: "/api/v1/users/{id}"
    };
  }
  
  // 列出用户
  rpc ListUsers(ListUsersRequest) returns (ListUsersResponse) {
    option (google.api.http) = {
      get: "/api/v1/users"
    };
  }
  
  // 更新用户
  rpc UpdateUser(UpdateUserRequest) returns (UpdateUserResponse) {
    option (google.api.http) = {
      put: "/api/v1/users/{id}"
      body: "*"
    };
  }
  
  // 删除用户
  rpc DeleteUser(DeleteUserRequest) returns (DeleteUserResponse) {
    option (google.api.http) = {
      delete: "/api/v1/users/{id}"
    };
  }
}

// 用户信息
message User {
  string id = 1;
  string name = 2;
  string email = 3;
  int64 created_at = 4;
  int64 updated_at = 5;
}

// 创建用户请求
message CreateUserRequest {
  string name = 1;
  string email = 2;
}

// 创建用户响应
message CreateUserResponse {
  User user = 1;
}

// 获取用户请求
message GetUserRequest {
  string id = 1;
}

// 获取用户响应
message GetUserResponse {
  User user = 1;
}

// 列出用户请求
message ListUsersRequest {
  int32 page = 1;
  int32 size = 2;
}

// 列出用户响应
message ListUsersResponse {
  repeated User users = 1;
  int32 total = 2;
}

// 更新用户请求
message UpdateUserRequest {
  string id = 1;
  string name = 2;
  string email = 3;
}

// 更新用户响应
message UpdateUserResponse {
  User user = 1;
}

// 删除用户请求
message DeleteUserRequest {
  string id = 1;
}

// 删除用户响应
message DeleteUserResponse {
  bool success = 1;
}
