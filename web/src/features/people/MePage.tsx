// /me: your own person page (the "Your profile" navigation entry), so the
// navigation needs no per-user link. Owner: P1-W03.
import { Navigate } from 'react-router';
import { Loading } from '../../app/Loading';
import { useSession } from '../../app/session';

export default function MePage() {
  const session = useSession();
  if (session.isPending) return <Loading />;
  const me = session.data?.me;
  return <Navigate to={me ? `/people/${me.ref}` : '/login'} replace />;
}
