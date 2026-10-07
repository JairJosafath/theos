import { useId, useState, type ReactElement } from 'react';

import { greeting } from './greeting';

interface AppProps {
  /** The app's name, shown as the page's heading. */
  readonly title: string;
}

/** The page: a name field and the greeting for it. */
export function App({ title }: AppProps): ReactElement {
  const [name, setName] = useState('');
  const nameId = useId();
  return (
    <main>
      <h1>{title}</h1>
      <label htmlFor={nameId}>Your name</label>
      <input
        id={nameId}
        value={name}
        onChange={(event) => {
          setName(event.target.value);
        }}
      />
      <p aria-live="polite">{greeting(name)}</p>
    </main>
  );
}
