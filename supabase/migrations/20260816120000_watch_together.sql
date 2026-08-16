create extension if not exists pgcrypto;

create table if not exists public.watch_rooms (
  code text primary key,
  host_id uuid not null references auth.users (id) on delete cascade,
  max_members smallint not null default 6 check (max_members between 2 and 8),
  created_at timestamptz not null default now(),
  expires_at timestamptz not null default now() + interval '6 hours'
);

create index if not exists watch_rooms_host_created_idx on public.watch_rooms (host_id, created_at desc);
create index if not exists watch_rooms_expires_idx on public.watch_rooms (expires_at);

create table if not exists public.watch_room_members (
  room_code text not null references public.watch_rooms (code) on delete cascade,
  user_id uuid not null references auth.users (id) on delete cascade,
  display_name text not null default 'Guest',
  joined_at timestamptz not null default now(),
  primary key (room_code, user_id)
);

create index if not exists watch_room_members_user_idx on public.watch_room_members (user_id);

alter table public.watch_rooms enable row level security;
alter table public.watch_room_members enable row level security;

create policy "members read their room"
  on public.watch_rooms for select to authenticated
  using (
    exists (
      select 1 from public.watch_room_members m
      where m.room_code = public.watch_rooms.code
        and m.user_id = (select auth.uid())
    )
  );

create policy "members read co-members"
  on public.watch_room_members for select to authenticated
  using (
    exists (
      select 1 from public.watch_room_members self
      where self.room_code = public.watch_room_members.room_code
        and self.user_id = (select auth.uid())
    )
  );

create or replace function public.watch_room_code()
returns text
language plpgsql
as $$
declare
  alphabet constant text := 'ABCDEFGHJKLMNPQRSTUVWXYZ23456789';
  raw bytea := gen_random_bytes(6);
  code text := '';
begin
  for index in 0..5 loop
    code := code || substr(alphabet, 1 + (get_byte(raw, index) % 32), 1);
  end loop;
  return code;
end;
$$;

create or replace function public.watch_room_purge()
returns void
language sql
security definer
set search_path = public
as $$
  delete from public.watch_rooms where expires_at <= now();
$$;

create or replace function public.create_watch_room(display_name text default 'Guest')
returns public.watch_rooms
language plpgsql
security definer
set search_path = public
as $$
declare
  actor uuid := auth.uid();
  candidate text;
  room public.watch_rooms;
begin
  if actor is null then
    raise exception 'authentication required' using errcode = '42501';
  end if;

  perform public.watch_room_purge();

  if (
    select count(*) from public.watch_rooms
    where host_id = actor and created_at > now() - interval '1 hour'
  ) >= 5 then
    raise exception 'room creation limit reached, try again later' using errcode = '53400';
  end if;

  delete from public.watch_rooms where host_id = actor;
  delete from public.watch_room_members where user_id = actor;

  for attempt in 1..32 loop
    candidate := public.watch_room_code();
    begin
      insert into public.watch_rooms (code, host_id)
      values (candidate, actor)
      returning * into room;
      exit;
    exception when unique_violation then
      room := null;
    end;
  end loop;

  if room.code is null then
    raise exception 'could not allocate a room code' using errcode = '53400';
  end if;

  insert into public.watch_room_members (room_code, user_id, display_name)
  values (room.code, actor, coalesce(nullif(btrim(display_name), ''), 'Guest'));

  return room;
end;
$$;

create or replace function public.join_watch_room(code text, display_name text default 'Guest')
returns public.watch_rooms
language plpgsql
security definer
set search_path = public
as $$
declare
  actor uuid := auth.uid();
  target text := upper(btrim(code));
  room public.watch_rooms;
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

  if exists (
    select 1 from public.watch_room_members
    where room_code = target and user_id = actor
  ) then
    return room;
  end if;

  if (select count(*) from public.watch_room_members where room_code = target) >= room.max_members then
    raise exception 'room is full' using errcode = '53400';
  end if;

  delete from public.watch_room_members where user_id = actor and room_code <> target;
  delete from public.watch_rooms where host_id = actor and public.watch_rooms.code <> target;

  insert into public.watch_room_members (room_code, user_id, display_name)
  values (target, actor, coalesce(nullif(btrim(display_name), ''), 'Guest'));

  return room;
end;
$$;

create or replace function public.leave_watch_room(code text)
returns void
language plpgsql
security definer
set search_path = public
as $$
declare
  actor uuid := auth.uid();
  target text := upper(btrim(code));
  successor uuid;
begin
  if actor is null then
    raise exception 'authentication required' using errcode = '42501';
  end if;

  delete from public.watch_room_members where room_code = target and user_id = actor;

  select user_id into successor from public.watch_room_members
  where room_code = target
  order by joined_at, user_id
  limit 1;

  if successor is null then
    delete from public.watch_rooms where public.watch_rooms.code = target;
  else
    update public.watch_rooms set host_id = successor
    where public.watch_rooms.code = target and host_id = actor;
  end if;
end;
$$;

revoke all on function public.create_watch_room(text) from public;
revoke all on function public.join_watch_room(text, text) from public;
revoke all on function public.leave_watch_room(text) from public;
revoke all on function public.watch_room_purge() from public;
grant execute on function public.create_watch_room(text) to authenticated;
grant execute on function public.join_watch_room(text, text) to authenticated;
grant execute on function public.leave_watch_room(text) to authenticated;

create policy "watch together members receive"
  on realtime.messages for select to authenticated
  using (
    realtime.topic() like 'watch-together:%'
    and exists (
      select 1 from public.watch_room_members m
      join public.watch_rooms r on r.code = m.room_code
      where m.room_code = split_part(realtime.topic(), ':', 2)
        and m.user_id = (select auth.uid())
        and r.expires_at > now()
    )
  );

create policy "watch together members send"
  on realtime.messages for insert to authenticated
  with check (
    realtime.topic() like 'watch-together:%'
    and exists (
      select 1 from public.watch_room_members m
      join public.watch_rooms r on r.code = m.room_code
      where m.room_code = split_part(realtime.topic(), ':', 2)
        and m.user_id = (select auth.uid())
        and r.expires_at > now()
    )
  );
