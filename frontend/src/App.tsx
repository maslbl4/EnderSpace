import { t } from './i18n/ru';

function App() {
  return (
    <main className="container">
      <header className="brand">
        <h1 className="title"><span className="title-accent">{t('appTitle')}</span></h1>
      </header>
      <p className="subtitle">{t('placeholder')}</p>
    </main>
  );
}

export default App;
