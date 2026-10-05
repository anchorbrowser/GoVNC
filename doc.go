// Package govnc is an RFB (VNC) server library that runs on any OS. It captures nothing and
// injects nothing: the caller publishes frames (a pre-encoded JPEG, sent to Tight clients without
// re-encoding, or an image.Image) and receives viewer input through per-session hooks.
//
// A Server serves any transport: Serve takes a net.Listener, ServeConn any io.ReadWriteCloser
// (a WebSocket, a tunnel stream, net.Pipe in tests). Each viewer is a Session, which can be sent
// clipboard text, given overlay images drawn over the frame, and asked for a full refresh.
//
// The protocol itself is in the rfb subpackage. Security defaults to None: bind to localhost or
// protect the transport.
package govnc
