package redisqueue

import (
	"context"
	"errors"

	"github.com/redis/go-redis/v9"
)

// Share 是比例调度中的一档。
// Priority 对应 Job.Priority,Count 是每个周期从这一档放行的任务数。
type Share struct {
	Priority int
	Count    int
}

// WithRatio 按份额轮转各优先级,而不是让数值更小的优先级一直排在前面。
// 每个周期按 shares 的声明顺序放行:某一档没有等待任务时跳过,不空等。
// 同一优先级内部仍按到达顺序。重复的 Priority 保留先声明的那一档;Count < 1 按 1 处理。
// 没有出现在 shares 里的优先级,排在已声明档位之后,每个周期放行 1 个,数值小的在前。
//
// 游标存在 Redis 里,消费同一个队列的 worker 都要带上同一份 WithRatio。只投递、不消费的进程不用配。
//
// 例:每 5 个普通任务(Priority 10)插 1 个 VIP(Priority 0):
//
//	redisqueue.WithRatio(
//	    redisqueue.Share{Priority: 10, Count: 5},
//	    redisqueue.Share{Priority: 0, Count: 1},
//	)
func WithRatio(shares ...Share) Option {
	return func(c *Config) {
		c.Shares = normalizeShares(shares)
	}
}

func normalizeShares(in []Share) []Share {
	seen := make(map[int]struct{}, len(in))
	out := make([]Share, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s.Priority]; ok {
			continue
		}
		seen[s.Priority] = struct{}{}
		if s.Count < 1 {
			s.Count = 1
		}
		out = append(out, s)
	}
	return out
}

// priorityBand 是相邻两个优先级之间的分数间隔。
// 等待队列 score = priority*priorityBand + seq。seq 更小的先出队;priority 更小的整段在前。
// float64 能精确表示的整数大约到 9e15,|priority| 建议不超过 1e5。seq 达到 priorityBand 后会被夹住,避免串到下一档。
const priorityBand int64 = 10_000_000_000

func waitingScore(priority int, seq int64) float64 {
	if seq < 0 {
		seq = 0
	}
	if seq >= priorityBand {
		seq = priorityBand - 1
	}
	return float64(int64(priority)*priorityBand + seq)
}

// ratioPopLua 按份额从 waiting 里取出一个任务。
// KEYS: waiting, priorities, ratio cursor, ratio left
// ARGV: band, 已声明档位数, 然后是 priority, count 成对出现。
const ratioPopLua = `
local scale = tonumber(ARGV[1])
local nconf = tonumber(ARGV[2])
local shares = {}
local configured = {}
for i = 1, nconf do
  local pri = tonumber(ARGV[2 + (i - 1) * 2 + 1])
  local cnt = tonumber(ARGV[2 + (i - 1) * 2 + 2])
  shares[#shares + 1] = {pri, cnt}
  configured[pri] = true
end

local known = redis.call('SMEMBERS', KEYS[2])
local extra = {}
for _, p in ipairs(known) do
  local pi = tonumber(p)
  if pi ~= nil and not configured[pi] then
    extra[#extra + 1] = pi
  end
end
table.sort(extra)
for _, pi in ipairs(extra) do
  shares[#shares + 1] = {pi, 1}
end

local n = #shares
if n == 0 then
  return nil
end

local cursor = redis.call('GET', KEYS[3])
local leftRaw = redis.call('GET', KEYS[4])
local left = 0
if leftRaw then
  left = tonumber(leftRaw) or 0
end
local curPri = nil
if cursor then
  curPri = tonumber(cursor)
end

local start = 1
if curPri ~= nil then
  for i = 1, n do
    if shares[i][1] == curPri then
      start = i
      break
    end
  end
end

local function pop_band(pri)
  local min = pri * scale
  local max = min + scale - 1
  local ids = redis.call('ZRANGEBYSCORE', KEYS[1], min, max, 'LIMIT', 0, 1)
  if ids == nil or #ids == 0 then
    return nil
  end
  redis.call('ZREM', KEYS[1], ids[1])
  return ids[1]
end

for attempt = 0, n - 1 do
  local idx = ((start - 1 + attempt) % n) + 1
  local pri = shares[idx][1]
  local count = shares[idx][2]
  local id = pop_band(pri)
  if id then
    local use = count
    if curPri ~= nil and pri == curPri and left > 0 then
      use = left
    end
    use = use - 1
    if use <= 0 then
      local nexti = idx % n + 1
      redis.call('SET', KEYS[3], shares[nexti][1])
      redis.call('SET', KEYS[4], shares[nexti][2])
    else
      redis.call('SET', KEYS[3], pri)
      redis.call('SET', KEYS[4], use)
    end
    return id
  end
end
return nil
`

var ratioPopScript = redis.NewScript(ratioPopLua)

func (q *Queue) dequeueRatio(ctx context.Context) (string, error) {
	args := make([]any, 0, 2+len(q.cfg.Shares)*2)
	args = append(args, priorityBand, len(q.cfg.Shares))
	for _, s := range q.cfg.Shares {
		args = append(args, s.Priority, s.Count)
	}
	res, err := ratioPopScript.Run(ctx, q.rdb, []string{
		q.key("waiting"),
		q.key("priorities"),
		q.key("ratio:cursor"),
		q.key("ratio:left"),
	}, args...).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", nil
		}
		return "", err
	}
	if res == nil {
		return "", nil
	}
	id, ok := res.(string)
	if !ok {
		return "", errors.New("redisqueue: ratio pop returned a non-string id")
	}
	return id, nil
}
