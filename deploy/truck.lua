-- OSRM profile for Sootcase road routing: OSRM's car profile (v26.8.0,
-- profiles/car.lua, unchanged apart from the parts marked TRUCK) plus
-- classes that let a request leave out roads a truck may not use.
--
-- Without an exclude, routes are the car profile's, as before. A request
-- that excludes a class leaves out every way in it (OSRM's `exclude`; MLD
-- keeps a metric per excludable set). The classes, by the OpenStreetMap
-- tags that put a way in them:
--
--   truck   hgv=no, hgv=destination, hgv=delivery (Sootcase reaches a
--           stop on such a street from the nearest road it may use, and
--           warns); maxheight(:physical) < 4.0 m; maxwidth(:physical)
--           < 2.60 m; maxlength(:hgv) < 18.75 m; maxweight(:hgv),
--           maxweightrating(:hgv) < 40 t; maxaxleload(:hgv) < 11.5 t
--   hzb     hazmat:B=no or hazmat:C=no (ADR 1.9.5 tunnel restriction
--           codes B and C: a load with code C is routed as code B,
--           leaving out a few more tunnels than it must)
--   hzd     hazmat:D=no
--   hze     hazmat:E=no; hazmat=no puts a way in all three
--   ferry   route=ferry (as in the car profile)
--
-- So a request is routed for a truck up to 4.0 m high, 2.60 m wide,
-- 18.75 m long, 40 t and 11.5 t per axle (Directive 96/53/EC's limits,
-- with the refrigerated width and the road-train length), and for each
-- ADR tunnel code. Smaller or lighter trucks get the same roads (a fixed
-- profile, which can leave out a road they could use); bigger ones can't
-- be routed. OSRM allows 7 classes and 6 excludable sets, hence one class
-- for size, weight and hgv access, codes B and C together, and no set for
-- dangerous goods with ferries left out.
-- *:conditional values count as always on. Units: m, cm, ft and in
-- (13'6"), t, kg, lbs, st (short tons); default, below_default, none,
-- unsigned and other words put no limit. Ferries are never in these
-- classes. Limits tagged on nodes (a height_restrictor barrier) can't go in
-- a class: only the car profile's own 2.0 m check applies to them.

api_version = 4

-- TRUCK: the OSRM image keeps the profile libraries in /opt/lib.
package.path = package.path .. ';/opt/?.lua'

Set = require('lib/set')
Sequence = require('lib/sequence')
Handlers = require("lib/way_handlers")
Relations = require("lib/relations")
Obstacles = require("lib/obstacles")
find_access_tag = require("lib/access").find_access_tag
resolve_access = require("lib/access").resolve_access
limit = require("lib/maxspeed").limit
Utils = require("lib/utils")
Measure = require("lib/measure")

function setup()
  return {
    properties = {
      max_speed_for_map_matching      = 180/3.6, -- 180kmph -> m/s
      -- For routing based on duration, but weighted for preferring certain roads
      weight_name                     = 'routability',
      -- For shortest duration without penalties for accessibility
      -- weight_name                     = 'duration',
      -- For shortest distance without penalties for accessibility
      -- weight_name                     = 'distance',
      process_call_tagless_node      = false,
      u_turn_penalty                 = 20,
      continue_straight_at_waypoint  = true,
      use_turn_restrictions          = true,
      left_hand_driving              = false,
    },

    default_mode              = mode.driving,
    default_speed             = 10,
    oneway_handling           = true,
    side_road_multiplier      = 0.8,
    turn_penalty              = 7.5,
    speed_reduction           = 0.8,
    turn_bias                 = 1.075,
    cardinal_directions       = false,

    -- Penalty multiplier for roads with no lane markings (lane_markings=no)
    -- Applied to bidirectional roads to prefer roads with clear lane markings
    lane_markings_penalty     = 0.75,

    -- Penalty multiplier for the disadvantaged direction on ways tagged 'priority=forward'/'priority=backward'.
    -- Applies to the per-direction rate (speed). A value < 1 reduces the disadvantaged
    -- direction's rate which increases its routing weight (weight ≈ duration / rate).
    -- This is applied to any way with a 'priority' tag; add an explicit width/lanes
    -- guard if the penalty should only target narrow or single-lane roads.
    priority_penalty          = 0.7,

    -- Size of the vehicle, to be limited by physical restriction of the way
    vehicle_height = 2.0, -- in meters, 2.0m is the height slightly above biggest SUVs
    vehicle_width = 1.9, -- in meters, ways with narrow tag are considered narrower than 2.2m

    -- Size of the vehicle, to be limited mostly by legal restriction of the way
    vehicle_length = 4.8, -- in meters, 4.8m is the length of large or family car
    vehicle_weight = 2000, -- in kilograms

    -- Optional: upper limit for all speeds (e.g., 87 for trucks)
    -- When set, no derived speed will exceed this value
    -- When nil (default), no additional capping is applied
    vehicle_max_speed = nil, -- in km/h

    -- a list of suffixes to suppress in name change instructions. The suffixes also include common substrings of each other
    suffix_list = {
      'N', 'NE', 'E', 'SE', 'S', 'SW', 'W', 'NW', 'North', 'South', 'West', 'East', 'Nor', 'Sou', 'We', 'Ea'
    },

    barrier_whitelist = Set {
      'cattle_grid',
      'border_control',
      'toll_booth',
      'sally_port',
      'no',
      'entrance',
      'height_restrictor',
      'arch'
    },

    access_tag_whitelist = Set {
      'yes',
      'motorcar',
      'motor_vehicle',
      'vehicle',
      'permissive',
      'designated',
      'hov'
    },

    access_tag_blacklist = Set {
      'no',
      'agricultural',
      'forestry',
      'emergency',
      'psv',
      'taxi', -- sub class of psv
      'share_taxi', -- sub class of psv
      'minibus', -- sub class of psv
      'bus', -- sub class of psv
      'foot',
      'emergency_vehicle',
      'restricted',
      'military',
      'official',
      'customers',
      'private',
      'delivery',
      'destination',
      'permit',
      'residents'
    },

    -- tags disallow access to in combination with highway=service
    service_access_tag_blacklist = Set {
        'private'
    },

    restricted_access_tag_list = Set {
      'private',
      'delivery',
      'destination',
      'customers',
      'permit',
      'residents',
      'unknown',
    },

    access_tags_hierarchy = Sequence {
      'motorcar',
      'motor_vehicle',
      'vehicle',
      'access'
    },

    service_tag_forbidden = Set {
      'emergency_access'
    },

    restrictions = Sequence {
      'motorcar',
      'motor_vehicle',
      'vehicle'
    },

    -- TRUCK: Sootcase's classes (see the top).
    classes = Sequence {
        'ferry', 'truck', 'hzb', 'hzd', 'hze'
    },

    -- TRUCK: the sets a request may exclude (OSRM allows 6). For a vehicle,
    -- Sootcase excludes truck, with the load's tunnel code or ferries.
    excludable = Sequence {
        Set {'ferry'},
        Set {'truck'},
        Set {'truck', 'ferry'},
        Set {'truck', 'hzb'},
        Set {'truck', 'hzd'},
        Set {'truck', 'hze'}
    },

    avoid = Set {
      'area',
      -- 'toll',    -- uncomment this to avoid tolls
      'reversible',
      'impassable',
      'hov_lanes',
      'steps',
      'construction',
      'proposed'
    },

    speeds = Sequence {
      highway = {
        motorway        = 90,
        motorway_link   = 45,
        trunk           = 85,
        trunk_link      = 40,
        primary         = 65,
        primary_link    = 30,
        secondary       = 55,
        secondary_link  = 25,
        tertiary        = 40,
        tertiary_link   = 20,
        unclassified    = 25,
        residential     = 25,
        living_street   = 10,
        service         = 15,
        -- winter highway types (OSM highway=winter_road / highway=ice_road)
        winter_road     = 20,
        ice_road        = 15
      }
    },

    service_penalties = {
      alley             = 0.5,
      parking           = 0.5,
      parking_aisle     = 0.5,
      driveway          = 0.5,
      ["drive-through"] = 0.5,
      ["drive-thru"] = 0.5
    },

    barrier_penalties = {
      gate      = 60,
      lift_gate = 60,
    },

    restricted_highway_whitelist = Set {
      'motorway',
      'motorway_link',
      'trunk',
      'trunk_link',
      'primary',
      'primary_link',
      'secondary',
      'secondary_link',
      'tertiary',
      'tertiary_link',
      'residential',
      'living_street',
      'unclassified',
      'service',
      'winter_road',
      'ice_road'
    },

    construction_whitelist = Set {
      'no',
      'widening',
      'minor',
    },

    route_speeds = {
      ferry = 5,
      shuttle_train = 10
    },

    bridge_speeds = {
      movable = 5
    },

    -- surface/trackype/smoothness
    -- values were estimated from looking at the photos at the relevant wiki pages

    -- max speed for surfaces
    surface_speeds = {
      asphalt = nil,    -- nil mean no limit. removing the line has the same effect
      concrete = nil,
      ["concrete:plates"] = nil,
      ["concrete:lanes"] = nil,
      paved = nil,

      cement = 80,
      compacted = 80,
      fine_gravel = 80,

      paving_stones = 60,
      metal = 60,
      bricks = 60,

      grass = 40,
      wood = 40,
      sett = 40,
      grass_paver = 40,
      gravel = 40,
      unpaved = 40,
      ground = 40,
      dirt = 40,
      pebblestone = 40,
      tartan = 40,

      cobblestone = 30,
      clay = 30,

      earth = 20,
      stone = 20,
      rocky = 20,
      sand = 20,

      laterite = 15,

      mud = 10,

      -- winter surfaces (OSM surface=ice / surface=snow)
      ice  = 20,
      snow = 30
    },

    -- max speed for tracktypes
    tracktype_speeds = {
      grade1 =  60,
      grade2 =  40,
      grade3 =  30,
      grade4 =  25,
      grade5 =  20
    },

    -- max speed for smoothnesses
    smoothness_speeds = {
      intermediate    =  80,
      bad             =  40,
      very_bad        =  20,
      horrible        =  10,
      very_horrible   =  5,
      impassable      =  0
    },

    -- http://wiki.openstreetmap.org/wiki/Speed_limits
    maxspeed_table_default = {
      urban = 50,
      rural = 90,
      trunk = 110,
      motorway = 130
    },

    -- List only exceptions
    maxspeed_table = {
      ["at:rural"] = 100,
      ["at:trunk"] = 100,
      ["ar:urban"] = 40,
      ["ar:rural"] = 110,      
      ["be:motorway"] = 120,
      ["be-bru:rural"] = 70,
      ["be-bru:urban"] = 30,
      ["be-vlg:rural"] = 70,
      ["bg:motorway"] = 140,
      ["by:urban"] = 60,
      ["by:motorway"] = 110,
      ["ca-on:rural"] = 80,
      ["ch:rural"] = 80,
      ["ch:trunk"] = 100,
      ["ch:motorway"] = 120,
      ["de:living_street"] = 7,
      ["de:rural"] = 100,
      ["de:motorway"] = 0,
      ["dk:rural"] = 80,
      ["es:trunk"] = 90,
      ["fr:rural"] = 80,
      ["gb:nsl_single"] = (60*1609)/1000,
      ["gb:nsl_dual"] = (70*1609)/1000,
      ["gb:motorway"] = (70*1609)/1000,
      ["lv:living_street"] = 20,
      ["nl:rural"] = 80,
      ["nl:trunk"] = 100,
      ['no:rural'] = 80,
      ['no:motorway'] = 110,
      ['ph:urban'] = 40,
      ['ph:rural'] = 80,
      ['ph:motorway'] = 100,
      ['pl:rural'] = 100,
      ['pl:expressway'] = 120,
      ['pl:motorway'] = 140,
      ["ro:trunk"] = 100,
      ["ru:living_street"] = 20,
      ["ru:urban"] = 60,
      ["ru:motorway"] = 110,
      ["uk:nsl_single"] = (60*1609)/1000,
      ["uk:nsl_dual"] = (70*1609)/1000,
      ["uk:motorway"] = (70*1609)/1000,
      ['za:urban'] = 60,
      ['za:rural'] = 100,
      ["none"] = 140
    },

    relation_types = Sequence {
      "route"
    },

    -- classify highway tags when necessary for turn weights
    highway_turn_classification = {
    },

    -- classify access tags when necessary for turn weights
    access_turn_classification = {
    }
  }
end

function process_node(profile, node, result, relations)
  -- parse access and barrier tags
  local access = resolve_access(find_access_tag(node, profile.access_tags_hierarchy), profile)
  if access then
    if profile.access_tag_blacklist[access] and not profile.restricted_access_tag_list[access] then
      obstacle_map:add(node, Obstacle.new(obstacle_type.barrier))
    end
  else
    local barrier = node:get_value_by_key("barrier")
    if barrier then
      --  check height restriction barriers
      local restricted_by_height = false
      if barrier == 'height_restrictor' then
         local maxheight = Measure.get_max_height(node:get_value_by_key("maxheight"), node)
         restricted_by_height = maxheight and maxheight < profile.vehicle_height
      end

      --  make an exception for rising bollard barriers
      local bollard = node:get_value_by_key("bollard")
      local rising_bollard = bollard and "rising" == bollard

      -- make an exception for lowered/flat barrier=kerb
      -- and incorrect tagging of highway crossing kerb as highway barrier
      local kerb = node:get_value_by_key("kerb")
      local highway = node:get_value_by_key("highway")
      local flat_kerb = kerb and ("lowered" == kerb or "flush" == kerb)
      local highway_crossing_kerb = barrier == "kerb" and highway and highway == "crossing"

      -- make an exception for fence with sensory=audible/audio (virtual livestock fences)
      local sensory = node:get_value_by_key("sensory")
      local audible_fence = barrier == "fence" and sensory and (sensory == "audible" or sensory == "audio")

      -- check if barrier has a configurable penalty (e.g., gates)
      local barrier_penalty = profile.barrier_penalties[barrier]

      if not profile.barrier_whitelist[barrier]
                and not rising_bollard
                and not flat_kerb
                and not highway_crossing_kerb
                and not audible_fence
                and not barrier_penalty
                or restricted_by_height then
        obstacle_map:add(node, Obstacle.new(obstacle_type.barrier))
      end

      -- apply configurable penalty to gates/lift_gates
      if barrier_penalty then
        obstacle_map:add(node, Obstacle.new(obstacle_type.gate,
                                            obstacle_direction.both, barrier_penalty, 0))
      end
    end
  end

  Obstacles.process_node(profile, node)
end

-- TRUCK: classes for the roads a truck may not use (see the top).
local TRUCK = { height = 4.0, width = 2.60, length = 18.75, weight = 40000, axle = 11500 }
local unlimited = Set { 'default', 'below_default', 'none', 'unsigned', 'no-sign', 'no_sign', 'no_indications', 'unknown' }

local function trim(v)
  return (v:lower():gsub('^%s+', ''):gsub('%s+$', ''))
end

-- A length in metres: 4, 4.0, "4,0 m", 3.8m, 380 cm, 13'6", 13 ft 6 in, 160". nil: no limit read.
function truck_length(v)
  v = trim(v)
  if unlimited[v] then return nil end
  local ft, inch = v:match("^(%d+%.?%d*)%s*'%s*(%d*%.?%d*)")
  if not ft then ft, inch = v:match('^(%d+%.?%d*)%s*ft%s*(%d*%.?%d*)') end
  if ft then return tonumber(ft) * 0.3048 + (tonumber(inch) or 0) * 0.0254 end
  local n = tonumber((v:gsub(',', '.')):match('^(%d+%.?%d*)'))
  if not n then return nil end
  if v:match('in$') or v:match('"$') then return n * 0.0254 end
  if v:match('cm$') then return n / 100 end
  return n
end

-- A mass in kilograms: 7.5, "7.5 t", 7500 kg, 16000 lbs, "7.5 st" (short tons). nil: no limit read.
function truck_mass(v)
  v = trim(v)
  if unlimited[v] then return nil end
  local n = tonumber((v:gsub(',', '.')):match('^(%d+%.?%d*)'))
  if not n then return nil end
  if v:match('kg$') then return n end
  if v:match('lbs?$') then return n * 0.45359237 end
  if v:match('st$') then return n * 907.18474 end
  if v:match('lt$') then return n * 1016.0469 end
  return n * 1000
end

-- The lowest value of any key in keys (with :forward, :backward and :conditional) in each direction.
local function lowest(way, keys, parse)
  local f, b
  local function take(raw, fwd, bwd)
    if not raw then return end
    for part in raw:gmatch('[^;]+') do
      local x = parse((part:gsub('@.*$', '')))
      if x then
        if fwd and (not f or x < f) then f = x end
        if bwd and (not b or x < b) then b = x end
      end
    end
  end
  for _, k in ipairs(keys) do
    take(way:get_value_by_key(k), true, true)
    take(way:get_value_by_key(k .. ':conditional'), true, true)
    take(way:get_value_by_key(k .. ':forward'), true, false)
    take(way:get_value_by_key(k .. ':backward'), false, true)
  end
  return f, b
end

-- The words a key gives in one direction (with :forward or :backward and :conditional).
local function words(way, key, fwd)
  local out = {}
  for _, k in ipairs({ key, key .. (fwd and ':forward' or ':backward'), key .. ':conditional' }) do
    local v = way:get_value_by_key(k)
    if v then
      for part in v:gmatch('[^;]+') do
        out[#out + 1] = trim((part:gsub('@.*$', '')))
      end
    end
  end
  return out
end

function truck_classes(way, result, data)
  if data.route == 'ferry' then return end
  local hf, hb = lowest(way, { 'maxheight', 'maxheight:physical' }, truck_length)
  local wf, wb = lowest(way, { 'maxwidth', 'maxwidth:physical' }, truck_length)
  local lf, lb = lowest(way, { 'maxlength', 'maxlength:hgv' }, truck_length)
  local mf, mb = lowest(way, { 'maxweight', 'maxweight:hgv', 'maxweightrating', 'maxweightrating:hgv' }, truck_mass)
  local af, ab = lowest(way, { 'maxaxleload', 'maxaxleload:hgv' }, truck_mass)
  for _, fwd in ipairs({ true, false }) do
    local classes = fwd and result.forward_classes or result.backward_classes
    local h, w, l, m, a = hf, wf, lf, mf, af
    if not fwd then h, w, l, m, a = hb, wb, lb, mb, ab end
    if (h and h < TRUCK.height) or (w and w < TRUCK.width) or (l and l < TRUCK.length) then classes['truck'] = true end
    if (m and m < TRUCK.weight) or (a and a < TRUCK.axle) then classes['truck'] = true end
    for _, v in ipairs(words(way, 'hgv', fwd)) do
      if v == 'no' then classes['truck'] = true
      elseif v == 'destination' or v == 'delivery' then classes['truck'] = true end
    end
    for _, v in ipairs(words(way, 'hazmat', fwd)) do
      if v == 'no' then classes['hzb'] = true; classes['hzd'] = true; classes['hze'] = true end
    end
    for _, code in ipairs({ 'B', 'C', 'D', 'E' }) do
      for _, v in ipairs(words(way, 'hazmat:' .. code, fwd)) do
        if v == 'no' then classes[code == 'C' and 'hzb' or 'hz' .. code:lower()] = true end
      end
    end
  end
end

function process_way(profile, way, result, relations)
  -- the intial filtering of ways based on presence of tags
  -- affects processing times significantly, because all ways
  -- have to be checked.
  -- to increase performance, prefetching and intial tag check
  -- is done in directly instead of via a handler.

  -- in general we should  try to abort as soon as
  -- possible if the way is not routable, to avoid doing
  -- unnecessary work. this implies we should check things that
  -- commonly forbids access early, and handle edge cases later.

  -- data table for storing intermediate values during processing
  local data = {
    -- prefetch tags
    highway = way:get_value_by_key('highway'),
    bridge = way:get_value_by_key('bridge'),
    route = way:get_value_by_key('route')
  }

  -- perform an quick initial check and abort if the way is
  -- obviously not routable.
  -- highway or route tags must be in data table, bridge is optional
  if (not data.highway or data.highway == '') and
  (not data.route or data.route == '')
  then
    return
  end

  handlers = Sequence {
    -- set the default mode for this profile. if can be changed later
    -- in case it turns we're e.g. on a ferry
    WayHandlers.default_mode,

    -- check various tags that could indicate that the way is not
    -- routable. this includes things like status=impassable,
    -- toll=yes and oneway=reversible
    WayHandlers.blocked_ways,
    WayHandlers.avoid_ways,
    WayHandlers.handle_height,
    WayHandlers.handle_width,
    WayHandlers.handle_length,
    WayHandlers.handle_weight,

    -- determine access status by checking our hierarchy of
    -- access tags, e.g: motorcar, motor_vehicle, vehicle
    WayHandlers.access,

    -- check whether forward/backward directions are routable
    WayHandlers.oneway,

    -- check a road's destination
    WayHandlers.destinations,

    -- check whether we're using a special transport mode
    WayHandlers.ferries,
    WayHandlers.movables,

    -- handle service road restrictions
    WayHandlers.service,

    -- handle hov
    WayHandlers.hov,

    -- compute speed taking into account way type, maxspeed tags, etc.
    WayHandlers.speed,
    WayHandlers.maxspeed,
    WayHandlers.surface,

    -- apply vehicle-specific maximum speed cap before calculating rates
    WayHandlers.vehicle_speed_cap,

    WayHandlers.penalties,

    -- compute class labels
    WayHandlers.classes,

    -- handle turn lanes and road classification, used for guidance
    WayHandlers.turn_lanes,
    WayHandlers.classification,

    -- handle various other flags
    WayHandlers.roundabouts,
    WayHandlers.startpoint,
    WayHandlers.driving_side,

    -- set name, ref and pronunciation
    WayHandlers.names,

    -- set weight properties of the way
    WayHandlers.weights,

    -- set classification of ways relevant for turns
    WayHandlers.way_classification_for_turn
  }

  WayHandlers.run(profile, way, result, data, handlers, relations)
  truck_classes(way, result, data) -- TRUCK

  if profile.cardinal_directions then
      Relations.process_way_refs(way, relations, result)
  end
end

function process_turn(profile, turn)
  -- Use a sigmoid function to return a penalty that maxes out at turn_penalty
  -- over the space of 0-180 degrees.  Values here were chosen by fitting
  -- the function to some turn penalty samples from real driving.
  local turn_penalty = profile.turn_penalty
  local turn_bias = turn.is_left_hand_driving and 1. / profile.turn_bias or profile.turn_bias

  for _, obs in pairs(obstacle_map:get(turn.from, turn.via)) do
    -- disregard a minor stop if entering by the major road
    -- rationale: if a stop sign is tagged at the center of the intersection with stop=minor
    -- it should only penalize the minor roads entering the intersection
    if obs.type == obstacle_type.stop_minor and not Obstacles.entering_by_minor_road(turn) then
        goto skip
    end
    -- heuristic to infer the direction of a stop without an explicit direction tag
    -- rationale: a stop sign should not be placed farther than 20m from the intersection
    if turn.number_of_roads == 2
        and obs.type == obstacle_type.stop
        and obs.direction == obstacle_direction.none
        and turn.source_road.distance < 20
        and turn.target_road.distance > 20 then
            goto skip
    end
    turn.duration = turn.duration + obs.duration
    ::skip::
  end

  if turn.number_of_roads > 2 or turn.source_mode ~= turn.target_mode or turn.is_u_turn then
    if turn.angle >= 0 then
      turn.duration = turn.duration + turn_penalty / (1 + math.exp( -((13 / turn_bias) *  turn.angle/180 - 6.5*turn_bias)))
    else
      turn.duration = turn.duration + turn_penalty / (1 + math.exp( -((13 * turn_bias) * -turn.angle/180 - 6.5/turn_bias)))
    end

    if turn.is_u_turn then
      turn.duration = turn.duration + profile.properties.u_turn_penalty
    end
  end

  -- for distance based routing we don't want to have penalties based on turn angle
  if profile.properties.weight_name == 'distance' then
     turn.weight = 0
  else
     turn.weight = turn.duration
  end

  if profile.properties.weight_name == 'routability' then
      -- penalize turns from non-local access only segments onto local access only tags
      if not turn.source_restricted and turn.target_restricted then
          turn.weight = constants.max_turn_weight
      end
  end
end

return {
  setup = setup,
  process_way = process_way,
  process_node = process_node,
  process_turn = process_turn
}
