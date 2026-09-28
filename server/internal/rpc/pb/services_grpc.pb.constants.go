package rpcpb

import grpc "google.golang.org/grpc"

const _ = grpc.SupportPackageIsVersion9

const (
	AuthService_Register_FullMethodName       = "/syncapp.rpc.v1.AuthService/Register"
	AuthService_Login_FullMethodName          = "/syncapp.rpc.v1.AuthService/Login"
	AuthService_Authenticate_FullMethodName   = "/syncapp.rpc.v1.AuthService/Authenticate"
	AuthService_Resume_FullMethodName         = "/syncapp.rpc.v1.AuthService/Resume"
	AuthService_ListSessions_FullMethodName   = "/syncapp.rpc.v1.AuthService/ListSessions"
	AuthService_RevokeOwned_FullMethodName    = "/syncapp.rpc.v1.AuthService/RevokeOwned"
	AuthService_RevokeAll_FullMethodName      = "/syncapp.rpc.v1.AuthService/RevokeAll"
	AuthService_DeleteAccount_FullMethodName  = "/syncapp.rpc.v1.AuthService/DeleteAccount"
	AuthService_LoginWithCode_FullMethodName  = "/syncapp.rpc.v1.AuthService/LoginWithCode"
	AuthService_ChangePassword_FullMethodName = "/syncapp.rpc.v1.AuthService/ChangePassword"
	AuthService_BeginTOTP_FullMethodName      = "/syncapp.rpc.v1.AuthService/BeginTOTP"
	AuthService_ConfirmTOTP_FullMethodName    = "/syncapp.rpc.v1.AuthService/ConfirmTOTP"
	AuthService_DisableTOTP_FullMethodName    = "/syncapp.rpc.v1.AuthService/DisableTOTP"
	AuthService_TwoFactorState_FullMethodName = "/syncapp.rpc.v1.AuthService/TwoFactorState"
)

// AuthServiceClient is the client API for AuthService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
var AuthService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "syncapp.rpc.v1.AuthService",
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
		{
			MethodName: "ListSessions",
			Handler:    _AuthService_ListSessions_Handler,
		},
		{
			MethodName: "RevokeOwned",
			Handler:    _AuthService_RevokeOwned_Handler,
		},
		{
			MethodName: "RevokeAll",
			Handler:    _AuthService_RevokeAll_Handler,
		},
		{
			MethodName: "DeleteAccount",
			Handler:    _AuthService_DeleteAccount_Handler,
		},
		{
			MethodName: "LoginWithCode",
			Handler:    _AuthService_LoginWithCode_Handler,
		},
		{
			MethodName: "ChangePassword",
			Handler:    _AuthService_ChangePassword_Handler,
		},
		{
			MethodName: "BeginTOTP",
			Handler:    _AuthService_BeginTOTP_Handler,
		},
		{
			MethodName: "ConfirmTOTP",
			Handler:    _AuthService_ConfirmTOTP_Handler,
		},
		{
			MethodName: "DisableTOTP",
			Handler:    _AuthService_DisableTOTP_Handler,
		},
		{
			MethodName: "TwoFactorState",
			Handler:    _AuthService_TwoFactorState_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "proto/syncapp/v1/services.proto",
}

const (
	ChatService_EnsureDirect_FullMethodName  = "/syncapp.rpc.v1.ChatService/EnsureDirect"
	ChatService_EnsureSecret_FullMethodName  = "/syncapp.rpc.v1.ChatService/EnsureSecret"
	ChatService_FindDirect_FullMethodName    = "/syncapp.rpc.v1.ChatService/FindDirect"
	ChatService_Get_FullMethodName           = "/syncapp.rpc.v1.ChatService/Get"
	ChatService_CreateGroup_FullMethodName   = "/syncapp.rpc.v1.ChatService/CreateGroup"
	ChatService_Members_FullMethodName       = "/syncapp.rpc.v1.ChatService/Members"
	ChatService_UserChats_FullMethodName     = "/syncapp.rpc.v1.ChatService/UserChats"
	ChatService_UserChatPage_FullMethodName  = "/syncapp.rpc.v1.ChatService/UserChatPage"
	ChatService_SetChatFlags_FullMethodName  = "/syncapp.rpc.v1.ChatService/SetChatFlags"
	ChatService_UserChatIDs_FullMethodName   = "/syncapp.rpc.v1.ChatService/UserChatIDs"
	ChatService_MemberIDs_FullMethodName     = "/syncapp.rpc.v1.ChatService/MemberIDs"
	ChatService_MemberIDsPage_FullMethodName = "/syncapp.rpc.v1.ChatService/MemberIDsPage"
	ChatService_CanPost_FullMethodName       = "/syncapp.rpc.v1.ChatService/CanPost"
	ChatService_IsMember_FullMethodName      = "/syncapp.rpc.v1.ChatService/IsMember"
	ChatService_CanModerate_FullMethodName   = "/syncapp.rpc.v1.ChatService/CanModerate"
)

// ChatServiceClient is the client API for ChatService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
var ChatService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "syncapp.rpc.v1.ChatService",
	HandlerType: (*ChatServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "EnsureDirect",
			Handler:    _ChatService_EnsureDirect_Handler,
		},
		{
			MethodName: "EnsureSecret",
			Handler:    _ChatService_EnsureSecret_Handler,
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
			MethodName: "UserChatPage",
			Handler:    _ChatService_UserChatPage_Handler,
		},
		{
			MethodName: "SetChatFlags",
			Handler:    _ChatService_SetChatFlags_Handler,
		},
		{
			MethodName: "UserChatIDs",
			Handler:    _ChatService_UserChatIDs_Handler,
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
		{
			MethodName: "CanModerate",
			Handler:    _ChatService_CanModerate_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "proto/syncapp/v1/services.proto",
}

const (
	MessageService_Submit_FullMethodName   = "/syncapp.rpc.v1.MessageService/Submit"
	MessageService_History_FullMethodName  = "/syncapp.rpc.v1.MessageService/History"
	MessageService_Thread_FullMethodName   = "/syncapp.rpc.v1.MessageService/Thread"
	MessageService_Forward_FullMethodName  = "/syncapp.rpc.v1.MessageService/Forward"
	MessageService_MarkRead_FullMethodName = "/syncapp.rpc.v1.MessageService/MarkRead"
)

// MessageServiceClient is the client API for MessageService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
var MessageService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "syncapp.rpc.v1.MessageService",
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
	Metadata: "proto/syncapp/v1/services.proto",
}

const (
	PresenceService_Online_FullMethodName    = "/syncapp.rpc.v1.PresenceService/Online"
	PresenceService_Heartbeat_FullMethodName = "/syncapp.rpc.v1.PresenceService/Heartbeat"
	PresenceService_Offline_FullMethodName   = "/syncapp.rpc.v1.PresenceService/Offline"
	PresenceService_Typing_FullMethodName    = "/syncapp.rpc.v1.PresenceService/Typing"
)

// PresenceServiceClient is the client API for PresenceService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
var PresenceService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "syncapp.rpc.v1.PresenceService",
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
	Metadata: "proto/syncapp/v1/services.proto",
}

const (
	KeyDirService_Publish_FullMethodName  = "/syncapp.rpc.v1.KeyDirService/Publish"
	KeyDirService_Fetch_FullMethodName    = "/syncapp.rpc.v1.KeyDirService/Fetch"
	KeyDirService_FetchAll_FullMethodName = "/syncapp.rpc.v1.KeyDirService/FetchAll"
)

// KeyDirServiceClient is the client API for KeyDirService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
var KeyDirService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "syncapp.rpc.v1.KeyDirService",
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
	Metadata: "proto/syncapp/v1/services.proto",
}
