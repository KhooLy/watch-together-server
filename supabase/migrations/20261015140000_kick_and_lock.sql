alter table public.watch_rooms
  add column if not exists locked boolean not null default false;

create table if not exists public.watch_room_bans (
  room_code text not null references public.watch_rooms (code) on delete cascade,
  user_id uuid not null,
  primary key (room_code, user_id)
);

alter table public.watch_room_bans enable row level security;

create or replace function public.kick_watch_room_member(code text, member uuid)
returns void
language plpgsql
security definer
set search_path = public
as $$
declare
  actor uuid := auth.uid();
  target text := upper(btrim(code));
begin
  if actor is null then
    raise exception 'authentication required' using errcode = '42501';
  end if;
  if member = actor then
    raise exception 'the host cannot remove themselves' using errcode = '42501';
  end if;
  if not exists (
    select 1 from public.watch_rooms r
    where r.code = target and r.host_id = actor and r.expires_at > now()
  ) then
    raise exception 'only the host can remove members' using errcode = '42501';
  end if;
  delete from public.watch_room_members where room_code = target and user_id = member;
  insert into public.watch_room_bans (room_code, user_id) values (target, member)
    on conflict do nothing;
end;
$$;

create or replace function public.set_watch_room_locked(code text, lock boolean)
returns void
language plpgsql
security definer
set search_path = public
as $$
declare
  actor uuid := auth.uid();
begin
  if actor is null then
    raise exception 'authentication required' using errcode = '42501';
  end if;
  update public.watch_rooms r set locked = lock
    where r.code = upper(btrim(code)) and r.host_id = actor and r.expires_at > now();
  if not found then
    raise exception 'only the host can lock the room' using errcode = '42501';
  end if;
end;
$$;

revoke all on function public.kick_watch_room_member(text, uuid) from public, anon;
revoke all on function public.set_watch_room_locked(text, boolean) from public, anon;
grant execute on function public.kick_watch_room_member(text, uuid) to authenticated;
grant execute on function public.set_watch_room_locked(text, boolean) to authenticated;

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

  if not found or exists (
    select 1 from public.watch_room_bans b where b.room_code = target and b.user_id = actor
  ) then
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

  if room.locked then
    raise exception 'room is locked' using errcode = '53400';
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
