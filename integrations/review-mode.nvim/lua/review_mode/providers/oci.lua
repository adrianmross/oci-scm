local M = {
  command = "oscm",
  env_prefix = "OSCM_REVIEW_",
  capabilities = { viewed = false, resolve = false },
}

local function meta(value)
  local pr = value.data or value
  return {
    repo = pr["repository-id"],
    number = pr.id,
    baseRefName = pr["destination-branch"],
    headRefName = pr["source-branch"],
    sourceRepository = pr["source-repository-id"] ~= vim.NIL and pr["source-repository-id"] or pr["repository-id"],
    title = pr["display-name"],
    state = pr["lifecycle-details"],
  }
end

local function view_args(ctx)
  local args = { "pr", "view" }
  if ctx.pr then
    args[#args + 1] = tostring(ctx.pr)
  end
  vim.list_extend(args, { "--json", "all" })
  return args
end

local function encode(value)
  return (value:gsub("[^%w_.~-]", function(c)
    return string.format("%%%02X", c:byte())
  end))
end

function M.metadata(ctx, callback)
  ctx.json(view_args(ctx), function(value, err)
    if not value then
      callback(nil, err)
      return
    end
    local pr = meta(value)
    if not pr.repo or not pr.number or not pr.headRefName or not pr.baseRefName then
      callback(nil, "OCI PR metadata is incomplete")
      return
    end
    ctx.json({
      "api",
      "/20210630/repositories/" .. encode(pr.sourceRepository) .. "/refs?refType=BRANCH&refName=" .. encode(
        pr.headRefName
      ),
      "--json",
      "all",
    }, function(refs, ref_err)
      local body = refs and refs.data
      if type(body) == "string" then
        local ok, decoded = pcall(vim.json.decode, body)
        body = ok and decoded or nil
      end
      for _, ref in ipairs(type(body) == "table" and body.items or {}) do
        if ref.refName == pr.headRefName and ref.refType == "BRANCH" then
          pr.headRefOid = ref.commitId
        end
      end
      if not pr.headRefOid then
        callback(nil, ref_err or "Could not verify OCI source branch head")
        return
      end
      callback({ repo = pr.repo, meta = pr }, nil)
    end)
  end)
end

-- Replies inherit root coordinates. Old-side comments cannot mark new-file lines.
function M.normalize_threads(comments)
  local by_id, threads, order = {}, {}, {}
  for _, comment in ipairs(comments) do
    if comment["lifecycle-state"] ~= "DELETED" then
      by_id[comment.id] = comment
    end
  end
  local function root(comment)
    local seen = {}
    while comment["parent-id"] and comment["parent-id"] ~= vim.NIL and comment["parent-id"] ~= "" do
      if seen[comment.id] or not by_id[comment["parent-id"]] then
        return nil
      end
      seen[comment.id] = true
      comment = by_id[comment["parent-id"]]
    end
    return comment
  end
  for _, comment in ipairs(comments) do
    if by_id[comment.id] then
      local anchor = root(comment)
      local path = anchor and anchor["file-path"]
      local line = anchor and tonumber(anchor["line-number"])
      if path and path ~= vim.NIL and line and line > 0 and anchor["file-type"] == "SOURCE" then
        local thread = threads[anchor.id]
        if not thread then
          thread = {
            id = anchor.id,
            path = path,
            line = line,
            isResolved = false,
            isOutdated = anchor.status == "OUTDATED",
            comments = { nodes = {} },
          }
          threads[anchor.id] = thread
          order[#order + 1] = thread
        end
        local author = comment["created-by"]
        if type(author) ~= "table" then
          author = {}
        end
        thread.comments.nodes[#thread.comments.nodes + 1] = {
          id = comment.id,
          body = comment.data,
          path = path,
          line = line,
          author = {
            login = author["principal-name"] ~= vim.NIL and author["principal-name"]
              or author["principal-id"]
              or "unknown",
          },
        }
      end
    end
  end
  return order
end

function M.threads(ctx, callback)
  ctx.json({ "pr", "comments", tostring(ctx.pr), "--json", "all" }, function(comments, err)
    if not comments then
      callback(nil, err)
      return
    end
    callback(M.normalize_threads(comments), nil)
  end)
end

function M.reply(ctx, target, body)
  return ctx.json_sync({
    "pr",
    "comment",
    tostring(ctx.pr),
    "--parent",
    target.thread_id or target.id,
    "--body",
    body,
    "--apply",
    "--json",
    "all",
  })
end

function M.comment(ctx, path, start_line, end_line, body)
  if start_line ~= end_line then
    return nil, "OCI supports one line anchor; select one line"
  end
  local head, err = ctx.system({ "git", "rev-parse", "HEAD" })
  if not head or not ctx.head or head ~= ctx.head then
    return nil, "OCI PR head differs from local HEAD; refresh or check out its source branch: " .. tostring(err or "")
  end
  local changed, diff_err = ctx.system({ "git", "diff", "--name-only", "HEAD", "--", path })
  if not changed or changed ~= "" then
    return nil, "Inline comments require an unchanged reviewed file: " .. tostring(diff_err or path)
  end
  return ctx.json_sync({
    "pr",
    "comment",
    tostring(ctx.pr),
    "--path",
    path,
    "--line",
    tostring(end_line),
    "--commit",
    head,
    "--side",
    "SOURCE",
    "--body",
    body,
    "--apply",
    "--json",
    "all",
  })
end

function M.status(ctx, callback)
  ctx.json(view_args(ctx), function(value, err)
    callback(value and meta(value), err)
  end)
end

function M.checks(ctx, callback)
  ctx.json({ "pr", "checks", tostring(ctx.pr), "--json", "all" }, function(value, err)
    callback(value and vim.json.encode(value), err)
  end)
end

function M.url(ctx, callback)
  local url = ctx.config.url
  if type(url) ~= "string" or not url:match("^https://") then
    callback(nil, "Set scm.url to the OCI PR console HTTPS URL")
    return
  end
  callback(url, nil)
end

return M
