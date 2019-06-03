-- ZREMRANGEBYSCORE msg/u:uid1/p:platform1/oms -inf (expirets
redis.call("ZREMRANGEBYSCORE", KEYS[1], "-inf", "("..ARGV[1])

local max_count = tonumber(ARGV[2])
if max_count>=0 then
    local right = tostring(max_count+1)

    -- ZRANGE msg/u:uid1/p:platform1/oms 0 -max_offline_msg_count
    local seqs = redis.call("ZRANGE", KEYS[1], "0", "-"..right)

    if table.getn(seqs) <=0 then 
        return nil
    end

    -- ZREMRANGEBYRANK msg/u:uid1/p:platform1/oms 0 -max_offline_msg_count
    redis.call("ZREMRANGEBYRANK", KEYS[1], "0", "-"..right)

    for i, seq in ipairs(seqs) do
        local nKey = "msg/om:"..seq.."/n"

        -- DECR msg/om:seq1/n
        if redis.call("DECR", nKey) <= 0 then
            local mKey = "msg/om:"..seq.."/m"
        
            -- DEL msg/om:seq1/m msg/om:seq1/n
            redis.call("DEL", mKey, nKey)
        end
    end 
end

return nil