package rpcpb

import grpc "google.golang.org/grpc"

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.64.0 or later.
const _ = grpc.SupportPackageIsVersion9

const (
	AuthService_Register_FullMethodName     = "/synapse.rpc.v1.AuthService/Register"
	AuthService_Login_FullMethodName        = "/synapse.rpc.v1.AuthService/Login"
	AuthService_Authenticate_FullMethodName = "/synapse.rpc.v1.AuthService/Authenticate"
	AuthService_Resume_FullMethodName       = "/synapse.rpc.v1.AuthService/Resume"
)

// AuthService_ServiceDesc is the grpc.ServiceDesc for AuthService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var AuthService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "synapse.rpc.v1.AuthService",
	HandlerType: (*AuthServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Register",
			Handler:    _AuthService_Register_Handler,
		},
		{
			MethodName: "Login",
			Handler:    _AuthService_Login_Handler,
		},
		{
			MethodName: "Authenticate",
			Handler:    _AuthService_Authenticate_Handler,
		},
		{
			MethodName: "Resume",
			Handler:    _AuthService_Resume_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "proto/synapse/v1/services.proto",
}

const (
	ChatService_EnsureDirect_FullMethodName  = "/synapse.rpc.v1.ChatService/EnsureDirect"
	ChatService_FindDirect_FullMethodName    = "/synapse.rpc.v1.ChatService/FindDirect"
	ChatService_Get_FullMethodName           = "/synapse.rpc.v1.ChatService/Get"
	ChatService_CreateGroup_FullMethodName   = "/synapse.rpc.v1.ChatService/CreateGroup"
	ChatService_Members_FullMethodName       = "/synapse.rpc.v1.ChatService/Members"
	ChatService_UserChats_FullMethodName     = "/synapse.rpc.v1.ChatService/UserChats"
	ChatService_MemberIDs_FullMethodName     = "/synapse.rpc.v1.ChatService/MemberIDs"
	ChatService_MemberIDsPage_FullMethodName = "/synapse.rpc.v1.ChatService/MemberIDsPage"
	ChatService_CanPost_FullMethodName       = "/synapse.rpc.v1.ChatService/CanPost"
	ChatService_IsMember_FullMethodName      = "/synapse.rpc.v1.ChatService/IsMember"
)

// ChatService_ServiceDesc is the grpc.ServiceDesc for ChatService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var ChatService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "synapse.rpc.v1.ChatService",
	HandlerType: (*ChatServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "EnsureDirect",
			Handler:    _ChatService_EnsureDirect_Handler,
		},
		{
			MethodName: "FindDirect",
			Handler:    _ChatService_FindDirect_Handler,
		},
		{
			MethodName: "Get",
			Handler:    _ChatService_Get_Handler,
		},
		{
			MethodName: "CreateGroup",
			Handler:    _ChatService_CreateGroup_Handler,
		},
		{
			MethodName: "Members",
			Handler:    _ChatService_Members_Handler,
		},
		{
			MethodName: "UserChats",
			Handler:    _ChatService_UserChats_Handler,
		},
		{
			MethodName: "MemberIDs",
			Handler:    _ChatService_MemberIDs_Handler,
		},
		{
			MethodName: "MemberIDsPage",
			Handler:    _ChatService_MemberIDsPage_Handler,
		},
		{
			MethodName: "CanPost",
			Handler:    _ChatService_CanPost_Handler,
		},
		{
			MethodName: "IsMember",
			Handler:    _ChatService_IsMember_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "proto/synapse/v1/services.proto",
}

const (
	MessageService_Submit_FullMethodName   = "/synapse.rpc.v1.MessageService/Submit"
	MessageService_History_FullMethodName  = "/synapse.rpc.v1.MessageService/History"
	MessageService_Thread_FullMethodName   = "/synapse.rpc.v1.MessageService/Thread"
	MessageService_Forward_FullMethodName  = "/synapse.rpc.v1.MessageService/Forward"
	MessageService_MarkRead_FullMethodName = "/synapse.rpc.v1.MessageService/MarkRead"
)

// MessageService_ServiceDesc is the grpc.ServiceDesc for MessageService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var MessageService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "synapse.rpc.v1.MessageService",
	HandlerType: (*MessageServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Submit",
			Handler:    _MessageService_Submit_Handler,
		},
		{
			MethodName: "History",
			Handler:    _MessageService_History_Handler,
		},
		{
			MethodName: "Thread",
			Handler:    _MessageService_Thread_Handler,
		},
		{
			MethodName: "Forward",
			Handler:    _MessageService_Forward_Handler,
		},
		{
			MethodName: "MarkRead",
			Handler:    _MessageService_MarkRead_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "proto/synapse/v1/services.proto",
}

const (
	PresenceService_Online_FullMethodName    = "/synapse.rpc.v1.PresenceService/Online"
	PresenceService_Heartbeat_FullMethodName = "/synapse.rpc.v1.PresenceService/Heartbeat"
	PresenceService_Offline_FullMethodName   = "/synapse.rpc.v1.PresenceService/Offline"
	PresenceService_Typing_FullMethodName    = "/synapse.rpc.v1.PresenceService/Typing"
)

// PresenceService_ServiceDesc is the grpc.ServiceDesc for PresenceService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var PresenceService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "synapse.rpc.v1.PresenceService",
	HandlerType: (*PresenceServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Online",
			Handler:    _PresenceService_Online_Handler,
		},
		{
			MethodName: "Heartbeat",
			Handler:    _PresenceService_Heartbeat_Handler,
		},
		{
			MethodName: "Offline",
			Handler:    _PresenceService_Offline_Handler,
		},
		{
			MethodName: "Typing",
			Handler:    _PresenceService_Typing_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "proto/synapse/v1/services.proto",
}

const (
	KeyDirService_Publish_FullMethodName  = "/synapse.rpc.v1.KeyDirService/Publish"
	KeyDirService_Fetch_FullMethodName    = "/synapse.rpc.v1.KeyDirService/Fetch"
	KeyDirService_FetchAll_FullMethodName = "/synapse.rpc.v1.KeyDirService/FetchAll"
)

// KeyDirService_ServiceDesc is the grpc.ServiceDesc for KeyDirService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var KeyDirService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "synapse.rpc.v1.KeyDirService",
	HandlerType: (*KeyDirServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Publish",
			Handler:    _KeyDirService_Publish_Handler,
		},
		{
			MethodName: "Fetch",
			Handler:    _KeyDirService_Fetch_Handler,
		},
		{
			MethodName: "FetchAll",
			Handler:    _KeyDirService_FetchAll_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "proto/synapse/v1/services.proto",
}
