package rpc

import (
	"github.com/synapse-chat/synapse/internal/message"
	pb "github.com/synapse-chat/synapse/internal/rpc/pb"
)

var opToPB = map[message.Op]pb.Op{
	message.OpCreate: pb.Op_OP_CREATE,
	message.OpEdit:   pb.Op_OP_EDIT,
	message.OpDelete: pb.Op_OP_DELETE,
}

var opFromPB = map[pb.Op]message.Op{
	pb.Op_OP_CREATE: message.OpCreate,
	pb.Op_OP_EDIT:   message.OpEdit,
	pb.Op_OP_DELETE: message.OpDelete,
}
