local root = assert(os.getenv("OSCM_PLUGIN_ROOT"))
vim.opt.runtimepath:prepend(root)
local provider = require("review_mode.providers.oci")
local comments = {
  { id = "reply", ["parent-id"] = "root", data = "reply body" },
  {
    id = "root",
    ["file-path"] = "a.go",
    ["line-number"] = 3,
    ["file-type"] = "SOURCE",
    status = "OUTDATED",
    data = "root body",
    ["created-by"] = { ["principal-name"] = "reviewer" },
  },
  { id = "old-side", ["file-path"] = "a.go", ["line-number"] = 3, ["file-type"] = "DESTINATION" },
  { id = "general", data = "general conversation" },
  { id = "orphan", ["parent-id"] = "missing" },
  { id = "cycle1", ["parent-id"] = "cycle2" },
  { id = "cycle2", ["parent-id"] = "cycle1" },
}
local threads = provider.normalize_threads(comments)
assert(#threads == 1 and #threads[1].comments.nodes == 2, "reply inheritance/filtering failed")
assert(threads[1].isOutdated and not threads[1].isResolved, "outdated was confused with resolved")
assert(threads[1].comments.nodes[2].author.login == "reviewer", "author lost")
local calls = {}
local ctx = {
  pr = "ocid1.devopspullrequest.fixture",
  head = "hash",
  config = {},
  system = function(args)
    return args[2] == "diff" and "" or "hash"
  end,
  json_sync = function(args)
    calls[#calls + 1] = args
    return { verified = true }
  end,
  json = function(args, callback)
    calls[#calls + 1] = args
    if args[1] == "pr" then
      callback({
        data = {
          id = "ocid1.devopspullrequest.fixture",
          ["repository-id"] = "repository",
          ["source-repository-id"] = "fork",
          ["source-branch"] = "feature/slash",
          ["destination-branch"] = "main",
        },
      })
    else
      assert(
        args[2]:find("/fork/refs?", 1, true) and args[2]:find("feature%2Fslash", 1, true),
        "fork/slash branch not handled"
      )
      callback({ data = { items = { { refName = "feature/slash", refType = "BRANCH", commitId = "hash" } } } })
    end
  end,
}
provider.metadata(ctx, function(value, err)
  assert(value and not err and value.meta.headRefOid == "hash", "remote head not verified")
end)
assert(provider.reply(ctx, { id = "reply", thread_id = "root" }, "literal body"))
assert(calls[#calls][5] == "root", "reply must target root")
assert(provider.comment(ctx, "a.go", 3, 3, "literal body"))
assert(
  vim.tbl_contains(calls[#calls], "--apply") and vim.tbl_contains(calls[#calls], "--commit"),
  "mutation gate or commit missing"
)
local count = #calls
assert(not provider.comment(ctx, "a.go", 2, 3, "range"))
ctx.system = function()
  return "different"
end
assert(not provider.comment(ctx, "a.go", 3, 3, "stale"))
assert(#calls == count, "invalid comment reached mutation")
ctx.json = function(_, callback)
  callback(nil, "auth failed")
end
provider.metadata(ctx, function(value, err)
  assert(not value and err == "auth failed", "auth error lost")
end)
provider.url(ctx, function(value, err)
  assert(not value and err, "URL guessed")
end)
assert(not provider.capabilities.viewed and not provider.capabilities.resolve)
print("OCI provider checks passed")
vim.cmd.qa()
