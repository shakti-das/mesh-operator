// msg.error(message, value) emits an error message that's treated as a user-input issue
// Usage:
//   local msg = import "lib/msg.jsonnet";
//   if bad_condition then
//     msg.error("description of the problem", [])
//   else
//     [{...resources...}]
{
  error(message, value):: std.native("msg_error")(message, value),
}
