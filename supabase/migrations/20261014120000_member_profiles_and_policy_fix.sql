alter table public.watch_room_members
  add column if not exists profile_id uuid,
  add column if not exists avatar text,
  add column if not exists color text;

alter table public.watch_room_members
  drop constraint if exists watch_room_members_avatar_check,
  add constraint watch_room_members_avatar_check
    check (avatar is null or char_length(avatar) <= 2048),
  drop constraint if exists watch_room_members_name_check,
  add constraint watch_room_members_name_check
    check (char_length(display_name) between 1 and 40);

create or replace function public.watch_room_identity(
  p_actor uuid,
  p_name text,
  p_profile_id uuid,
  p_avatar text,
  out o_name text,
  out o_avatar text,
  out o_color text,
  out o_profile uuid
)
language plpgsql
security definer
set search_path = public
as $$
begin
  o_name := coalesce(nullif(left(btrim(coalesce(p_name, '')), 40), ''), 'Guest');
  o_avatar := null;
  o_color := null;
  o_profile := null;

  if p_avatar is not null
     and char_length(p_avatar) <= 2048
     and p_avatar ~ '^https://[^@[:space:]]+$' then
    o_avatar := p_avatar;
  end if;
end;
$$;

drop function if exists public.create_watch_room(text);
drop function if exists public.join_watch_room(text, text);

create or replace function public.create_watch_room(
  display_name text default 'Guest',
  profile_id uuid default null,
  avatar text default null
)
returns public.watch_rooms
language plpgsql
security definer
set search_path = public
as $$
declare
  actor uuid := auth.uid();
  limits public.watch_settings;
  candidate text;
  room public.watch_rooms;
  who record;
begin
  if actor is null then
    raise exception 'authentication required' using errcode = '42501';
  end if;

  perform public.watch_room_purge();
  delete from public.watch_room_creations where created_at < now() - interval '1 day';

  select * into limits from public.watch_settings where id;

  if (
    select count(*) from public.watch_room_creations
    where host_id = actor and created_at > now() - interval '1 hour'
  ) >= limits.rooms_per_hour then
    raise exception 'room creation limit reached, try again later' using errcode = '53400';
  end if;

  perform public.watch_room_release(actor);

  for attempt in 1..32 loop
    candidate := public.watch_room_code();
    begin
      insert into public.watch_rooms (code, host_id, max_members, expires_at)
      values (candidate, actor, limits.max_members, now() + limits.room_ttl)
      returning * into room;
      exit;
    exception when unique_violation then
      room := null;
    end;
  end loop;

  if room.code is null then
    raise exception 'could not allocate a room code' using errcode = '53400';
  end if;

  insert into public.watch_room_creations (host_id) values (actor);

  select * into who from public.watch_room_identity(actor, display_name, profile_id, avatar);

  insert into public.watch_room_members (room_code, user_id, display_name, profile_id, avatar, color)
  values (room.code, actor, who.o_name, who.o_profile, who.o_avatar, who.o_color);

  return room;
end;
$$;

create or replace function public.join_watch_room(
  code text,
  display_name text default 'Guest',
  profile_id uuid default null,
  avatar text default null
)
returns public.watch_rooms
language plpgsql
security definer
set search_path = public
as $$
declare
  actor uuid := auth.uid();
  target text := upper(btrim(code));
  room public.watch_rooms;
  who record;
begin
  if actor is null then
    raise exception 'authentication required' using errcode = '42501';
  end if;

  select * into room from public.watch_rooms
  where public.watch_rooms.code = target and expires_at > now()
  for update;

  if not found then
    raise exception 'room not found' using errcode = 'P0002';
  end if;

  select * into who from public.watch_room_identity(actor, display_name, profile_id, avatar);

  if exists (
    select 1 from public.watch_room_members
    where room_code = target and user_id = actor
  ) then
    update public.watch_room_members
      set display_name = who.o_name, profile_id = who.o_profile, avatar = who.o_avatar, color = who.o_color
      where room_code = target and user_id = actor;
    return room;
  end if;

  if (select count(*) from public.watch_room_members where room_code = target) >= room.max_members then
    raise exception 'room is full' using errcode = '53400';
  end if;

  perform public.watch_room_release(actor, target);

  insert into public.watch_room_members (room_code, user_id, display_name, profile_id, avatar, color)
  values (target, actor, who.o_name, who.o_profile, who.o_avatar, who.o_color);

  return room;
end;
$$;

revoke all on function public.watch_room_identity(uuid, text, uuid, text) from public, anon, authenticated;
revoke all on function public.create_watch_room(text, uuid, text) from public, anon;
revoke all on function public.join_watch_room(text, text, uuid, text) from public, anon;
revoke all on function public.leave_watch_room(text) from anon;
revoke all on function public.watch_room_purge() from anon, authenticated;
grant execute on function public.create_watch_room(text, uuid, text) to authenticated;
grant execute on function public.join_watch_room(text, text, uuid, text) to authenticated;

alter function public.watch_room_code() set search_path = public, extensions;

create or replace function public.is_watch_room_member(p_code text)
returns boolean
language sql
stable
security definer
set search_path = public
as $$
  select exists (
    select 1
    from public.watch_room_members m
    join public.watch_rooms r on r.code = m.room_code
    where m.room_code = p_code
      and m.user_id = (select auth.uid())
      and r.expires_at > now()
  );
$$;

revoke all on function public.is_watch_room_member(text) from public, anon;
grant execute on function public.is_watch_room_member(text) to authenticated;

drop policy if exists "members read their room" on public.watch_rooms;
drop policy if exists "members read co-members" on public.watch_room_members;
drop policy if exists "watch together members receive" on realtime.messages;
drop policy if exists "watch together members send" on realtime.messages;

create policy "members read their room"
  on public.watch_rooms for select to authenticated
  using (public.is_watch_room_member(code));

create policy "members read co-members"
  on public.watch_room_members for select to authenticated
  using (public.is_watch_room_member(room_code));

create policy "watch together members receive"
  on realtime.messages for select to authenticated
  using (
    realtime.topic() like 'watch-together:%'
    and public.is_watch_room_member(split_part(realtime.topic(), ':', 2))
  );

create policy "watch together members send"
  on realtime.messages for insert to authenticated
  with check (
    realtime.topic() like 'watch-together:%'
    and public.is_watch_room_member(split_part(realtime.topic(), ':', 2))
  );

create or replace function public.watch_cleanup_anonymous_users()
returns void
language sql
security definer
set search_path = public
as $$
  delete from auth.users u
  where u.is_anonymous
    and u.created_at < now() - interval '7 days'
    and not exists (select 1 from public.watch_room_members m where m.user_id = u.id);
$$;

revoke all on function public.watch_cleanup_anonymous_users() from public, anon, authenticated;

do $$
begin
  if exists (select 1 from pg_extension where extname = 'pg_cron') then
    perform cron.schedule('watch_cleanup_anonymous_users', '17 4 * * *', 'select public.watch_cleanup_anonymous_users()');
    perform cron.schedule('watch_room_purge', '*/30 * * * *', 'select public.watch_room_purge()');
  end if;
end;
$$;
