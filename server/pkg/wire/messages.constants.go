package wire

// bodyCodec is the active codec. Default JSON; replace via SetBodyCodec.
var bodyCodec BodyCodec = jsonCodec{}
