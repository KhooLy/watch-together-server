create or replace function public.watch_can_send(p_topic text)
returns boolean
language sql
stable
security definer
set search_path = public
as $$
  select case split_part(p_topic, ':', 3)
    when 'h' then
      split_part(p_topic, ':', 4) = (select auth.uid())::text
      and exists (
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
