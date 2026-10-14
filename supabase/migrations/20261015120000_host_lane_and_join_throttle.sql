create table if not exists public.watch_join_failures (
  user_id uuid not null,
  created_at timestamptz not null default now()
);

create index if not exists watch_join_failures_user_idx on public.watch_join_failures (user_id, created_at desc);

alter table public.watch_join_failures enable row level security;

create or replace function public.watch_can_send(p_topic text)
returns boolean
language sql
stable
security definer
set search_path = public
as $$
  select case split_part(p_topic, ':', 3)
    when 'host' then exists (
      select 1 from public.watch_rooms r
      where r.code = split_part(p_topic, ':', 2)
        and r.host_id = (select auth.uid())
        and r.expires_at > now()
    )
    when 'u' then
      split_part(p_topic, ':', 4) = (select auth.uid())::text
      and public.is_watch_room_member(split_part(p_topic, ':', 2))
    else false
  end;
$$;

revoke all on function public.watch_can_send(text) from public, anon;
grant execute on function public.watch_can_send(text) to authenticated;

drop policy if exists "watch together members receive" on realtime.messages;
drop policy if exists "watch together members send" on realtime.messages;

create policy "watch together members receive"
  on realtime.messages for select to authenticated
  using (
    realtime.topic() like 'watch-together:%'
    and public.is_watch_room_member(split_part(realtime.topic(), ':', 2))
  );

create policy "watch together lanes send"
  on realtime.messages for insert to authenticated
  with check (
    realtime.topic() like 'watch-together:%'
    and (
      (realtime.messages.extension = 'presence'
        and public.is_watch_room_member(split_part(realtime.topic(), ':', 2)))
      or (realtime.messages.extension = 'broadcast'
        and public.watch_can_send(realtime.topic()))
    )
  );

create or replace function public.watch_room_purge()
returns void
language sql
security definer
set search_path = public
as $$
  delete from public.watch_rooms where expires_at <= now();
  delete from public.watch_join_failures where created_at < now() - interval '1 hour';
$$;

revoke all on function public.watch_room_purge() from public, anon, authenticated;

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

  if (
    select count(*) from public.watch_join_failures
    where user_id = actor and created_at > now() - interval '10 minutes'
  ) >= 8 then
    raise exception 'too many attempts, try again later' using errcode = '53400';
  end if;

  select * into room from public.watch_rooms
  where public.watch_rooms.code = target and expires_at > now()
  for update;

  if not found then
    insert into public.watch_join_failures (user_id) values (actor);
    return null;
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

revoke all on function public.join_watch_room(text, text, uuid, text) from public, anon;
grant execute on function public.join_watch_room(text, text, uuid, text) to authenticated;

revoke all on function public.create_default_fluxa_profile() from public, anon, authenticated;
revoke all on function public.record_watched_item_delta() from public, anon, authenticated;
revoke all on function public.reject_anonymous_profile() from public, anon, authenticated;
